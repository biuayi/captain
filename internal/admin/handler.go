// Package admin serves the super-admin backend: organizer account management,
// granular permissions, platform secret config, audit, DB export.
// Auth domain is physically separate from organizer (DESIGN §1/§SS-0).
package admin

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/hertz/captain/internal/audit"
	"github.com/hertz/captain/internal/export"
	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/loginguard"
	"github.com/hertz/captain/internal/orgperm"
	"github.com/hertz/captain/internal/platformcfg"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/storage"
	"github.com/hertz/captain/internal/templatecache"
	"github.com/hertz/captain/internal/token"
	"github.com/hertz/captain/internal/turnstile"
	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	Repo   *repo.Repo
	Sig    *token.Signer
	Guard  *loginguard.Guard
	TS     *turnstile.Verifier
	PC     *platformcfg.Manager
	OrgPC  *orgperm.Cache
	Export *export.Worker
	Store  storage.Storage
	TplC   *templatecache.Cache
}

// assetMIMEAllow is the upload MIME whitelist (no executables; DESIGN §SS-1).
var assetMIMEAllow = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"image/svg+xml": true, "font/woff2": true, "font/woff": true,
	"application/json": true, "text/css": true,
}

// ConfigKeys are the platform secret slots a super-admin can set (DESIGN §3.3).
var ConfigKeys = []string{
	"cloudflare_turnstile_sitekey",
	"cloudflare_turnstile_secret",
	"aliyun_oss_endpoint",
	"aliyun_oss_bucket",
	"aliyun_oss_key_id",
	"aliyun_oss_key_secret",
}

type loginReq struct {
	LoginName string `json:"login_name"`
	Password  string `json:"password"`
	Turnstile string `json:"turnstile_token"`
}

// Login: POST /api/v1/{adminslug}/login — constant 3s delay + 10/10min
// lockout + optional Turnstile (DESIGN §SS-2 hardening, reused).
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	ctx := r.Context()
	loginguard.Wait(ctx)
	const scope = "admin"
	if h.Guard.Locked(ctx, scope, req.LoginName) {
		httpx.Fail(w, http.StatusLocked, "account_locked", "账号已锁定，请稍后再试")
		return
	}
	if !h.TS.Verify(ctx, req.Turnstile, httpx.ClientIP(r)) {
		httpx.Fail(w, http.StatusForbidden, "captcha_failed", "人机验证未通过")
		return
	}
	c, err := h.Repo.AdminByLogin(ctx, req.LoginName)
	if err != nil || c.Status != "active" ||
		bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(req.Password)) != nil {
		h.Guard.RecordFailure(ctx, scope, req.LoginName)
		httpx.Fail(w, http.StatusUnauthorized, "bad_credentials", "登录失败")
		return
	}
	h.Guard.Reset(ctx, scope, req.LoginName)
	tok, _ := h.Sig.Sign(token.Claims{
		Kind: token.KindAuth, Role: token.RoleAdmin, Subject: c.ID,
		ExpiresAt: time.Now().Add(12 * time.Hour).Unix(),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"token": tok})
}

// auth verifies the admin bearer token and returns the admin id.
func (h *Handler) auth(w http.ResponseWriter, r *http.Request) (string, bool) {
	c, ok := httpx.AuthClaims(r, h.Sig, token.RoleAdmin)
	if !ok {
		httpx.Fail(w, http.StatusUnauthorized, "unauthorized", "需要超管登录")
		return "", false
	}
	return c.Subject, true
}

func (h *Handler) audit(r *http.Request, actorID, action, target string, meta map[string]any) {
	_ = h.Repo.AppendAudit(r.Context(), audit.Entry{
		ActorRole: "admin", ActorID: actorID, Action: action, Target: target,
		Meta: meta, RequestID: httpx.RequestIDOf(r.Context()),
	})
}

// ---- organizer management ----

// ListOrganizers: GET /organizers
func (h *Handler) ListOrganizers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.auth(w, r); !ok {
		return
	}
	orgs, err := h.Repo.ListOrganizers(r.Context())
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "query failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"organizers": orgs})
}

type createOrgReq struct {
	Name      string `json:"name"`
	LoginName string `json:"login_name"`
	Password  string `json:"password"`
}

// CreateOrganizer: POST /organizers
func (h *Handler) CreateOrganizer(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.auth(w, r)
	if !ok {
		return
	}
	var req createOrgReq
	if err := httpx.DecodeJSON(r, &req); err != nil || req.LoginName == "" || req.Password == "" {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "name/login_name/password 必填")
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	id, err := h.Repo.CreateOrganizer(r.Context(), req.Name, req.LoginName, string(hash))
	if err != nil {
		httpx.Fail(w, http.StatusConflict, "conflict", "登录名已存在或创建失败")
		return
	}
	h.audit(r, adminID, "organizer_create", id, map[string]any{"login": req.LoginName})
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

type statusReq struct {
	Status string `json:"status"`
}

// SetOrganizerStatus: POST /organizers/{id}/status
func (h *Handler) SetOrganizerStatus(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.auth(w, r)
	if !ok {
		return
	}
	var req statusReq
	if err := httpx.DecodeJSON(r, &req); err != nil ||
		(req.Status != "active" && req.Status != "disabled") {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "status 必须为 active|disabled")
		return
	}
	id := r.PathValue("id")
	if err := h.Repo.SetOrganizerStatus(r.Context(), id, req.Status); err != nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动方不存在")
		return
	}
	h.audit(r, adminID, "organizer_status", id, map[string]any{"status": req.Status})
	httpx.JSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

// DeleteOrganizer: DELETE /organizers/{id} — soft delete (SS0-03).
func (h *Handler) DeleteOrganizer(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.auth(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := h.Repo.SoftDeleteOrganizer(r.Context(), id); err != nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动方不存在或已删除")
		return
	}
	h.OrgPC.Invalidate(r.Context(), id)
	h.audit(r, adminID, "organizer_delete", id, nil)
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type pwReq struct {
	Password string `json:"password"`
}

// ResetOrganizerPassword: POST /organizers/{id}/password (SS0-05).
func (h *Handler) ResetOrganizerPassword(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.auth(w, r)
	if !ok {
		return
	}
	var req pwReq
	if err := httpx.DecodeJSON(r, &req); err != nil || len(req.Password) < 6 {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "password 至少 6 位")
		return
	}
	id := r.PathValue("id")
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err := h.Repo.ResetOrganizerPassword(r.Context(), id, string(hash)); err != nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动方不存在")
		return
	}
	h.audit(r, adminID, "organizer_password_reset", id, nil)
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type permReq struct {
	CanCreateEvent   bool `json:"can_create_event"`
	CanViewRecords   bool `json:"can_view_records"`
	CanExportRecords bool `json:"can_export_records"`
}

// SetOrganizerPermissions: PATCH /organizers/{id}/permissions (SS0-07).
func (h *Handler) SetOrganizerPermissions(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.auth(w, r)
	if !ok {
		return
	}
	var req permReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	id := r.PathValue("id")
	pv, err := h.Repo.SetOrganizerPermissions(r.Context(), id,
		req.CanCreateEvent, req.CanViewRecords, req.CanExportRecords)
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动方不存在")
		return
	}
	h.OrgPC.Invalidate(r.Context(), id) // force fresh perm check next request
	h.audit(r, adminID, "organizer_permissions", id, map[string]any{
		"can_create_event": req.CanCreateEvent, "can_view_records": req.CanViewRecords,
		"can_export_records": req.CanExportRecords, "perm_version": pv,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"perm_version": pv})
}

// ---- platform config (encrypted secrets) ----

// GetConfig: GET /config — set flag + tail mask only, never plaintext (SS0-11).
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.auth(w, r); !ok {
		return
	}
	masks, _ := h.Repo.ListPlatformConfigKeys(r.Context())
	out := map[string]any{}
	for _, k := range ConfigKeys {
		m, inDB := masks[k]
		entry := map[string]any{"set": inDB, "masked": m, "source": "db"}
		if !inDB {
			_, src := h.PC.Get(r.Context(), k)
			entry["set"] = src != "none"
			entry["source"] = src
		}
		out[k] = entry
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"config": out})
}

type cfgReq struct {
	Value string `json:"value"`
}

// PutConfig: PUT /config/{key} — encrypt + persist (SS0-12).
func (h *Handler) PutConfig(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.auth(w, r)
	if !ok {
		return
	}
	key := r.PathValue("key")
	if !slices.Contains(ConfigKeys, key) {
		httpx.Fail(w, http.StatusBadRequest, "bad_key", "未知配置项")
		return
	}
	var req cfgReq
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Value == "" {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "value 必填")
		return
	}
	if !h.PC.Enabled() {
		httpx.Fail(w, http.StatusServiceUnavailable, "config_disabled",
			"未设置 CAPTAIN_CONFIG_KEY，无法安全存储密钥")
		return
	}
	if err := h.PC.Set(r.Context(), key, req.Value, adminID); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "保存失败")
		return
	}
	h.audit(r, adminID, "config_set", key, nil) // never log the value
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- audit query ----

// ListAudit: GET /audit?action=&from=&to=&limit= (SS0-17).
func (h *Handler) ListAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.auth(w, r); !ok {
		return
	}
	q := r.URL.Query()
	var from, to time.Time
	if v := q.Get("from"); v != "" {
		from, _ = time.Parse(time.RFC3339, v)
	}
	if v := q.Get("to"); v != "" {
		to, _ = time.Parse(time.RFC3339, v)
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	rows, err := h.Repo.ListAudit(r.Context(), q.Get("action"), from, to, limit)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "query failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"audit": rows, "total": len(rows)})
}

// ---- DB export ----

// CreateDBExport: POST /db-export (SS0-16).
func (h *Handler) CreateDBExport(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.auth(w, r)
	if !ok {
		return
	}
	jobID, err := h.Repo.CreateDBExportJob(r.Context())
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "create job failed")
		return
	}
	if err := h.Export.Request(r.Context(), jobID); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "mq", "enqueue failed")
		return
	}
	h.audit(r, adminID, "db_export", jobID, nil)
	httpx.JSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "pending"})
}

// DBExportStatus: GET /db-export/{job_id} (SS0-16).
func (h *Handler) DBExportStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.auth(w, r); !ok {
		return
	}
	job, err := h.Repo.DBExportJob(r.Context(), r.PathValue("job_id"))
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "任务不存在")
		return
	}
	httpx.JSON(w, http.StatusOK, job)
}

// ---- SS-1: template registry ----

// ListTemplates: GET /templates?kind= (SS1-05).
func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.auth(w, r); !ok {
		return
	}
	ts, err := h.Repo.ListTemplates(r.Context(), r.URL.Query().Get("kind"))
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "query failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"templates": ts})
}

type createTplReq struct {
	Kind        string         `json:"kind"`
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Version     int            `json:"version"`
	OrganizerID string         `json:"organizer_id"`
	Manifest    map[string]any `json:"manifest"`
}

// CreateTemplate: POST /templates (SS1-05).
func (h *Handler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.auth(w, r)
	if !ok {
		return
	}
	var req createTplReq
	if err := httpx.DecodeJSON(r, &req); err != nil ||
		(req.Kind != "screen" && req.Kind != "flow_page") || req.Code == "" || req.Name == "" {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "kind(screen|flow_page)/code/name 必填")
		return
	}
	if req.Version <= 0 {
		req.Version = 1
	}
	var org *string
	if req.OrganizerID != "" {
		org = &req.OrganizerID
	}
	man, _ := json.Marshal(req.Manifest)
	id, err := h.Repo.CreateTemplate(r.Context(), req.Kind, req.Code, req.Name, req.Version, org, man)
	if err != nil {
		httpx.Fail(w, http.StatusConflict, "conflict", "code+version 已存在或创建失败")
		return
	}
	h.TplC.Invalidate(r.Context())
	h.audit(r, adminID, "template_create", id, map[string]any{"kind": req.Kind, "code": req.Code})
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

type tplStatusReq struct {
	Status string `json:"status"`
}

// UpdateTemplate: PUT /templates/{id} {status} (publish/disable) (SS1-05).
func (h *Handler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.auth(w, r)
	if !ok {
		return
	}
	var req tplStatusReq
	if err := httpx.DecodeJSON(r, &req); err != nil ||
		(req.Status != "draft" && req.Status != "published" && req.Status != "disabled") {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "status 须为 draft|published|disabled")
		return
	}
	id := r.PathValue("id")
	if err := h.Repo.UpdateTemplateStatus(r.Context(), id, req.Status); err != nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "模板不存在")
		return
	}
	h.TplC.Invalidate(r.Context())
	h.audit(r, adminID, "template_status", id, map[string]any{"status": req.Status})
	httpx.JSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

// DeleteTemplate: DELETE /templates/{id} — soft (status=disabled) (SS1-05).
func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.auth(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := h.Repo.UpdateTemplateStatus(r.Context(), id, "disabled"); err != nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "模板不存在")
		return
	}
	h.TplC.Invalidate(r.Context())
	h.audit(r, adminID, "template_delete", id, nil)
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

// UploadTemplateAsset: POST /templates/{id}/assets (multipart, MIME
// whitelist) → object storage (SS1-06).
func (h *Handler) UploadTemplateAsset(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.auth(w, r)
	if !ok {
		return
	}
	tplID := r.PathValue("id")
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "需 multipart 上传")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "缺少 file 字段")
		return
	}
	defer file.Close()
	mimeType := hdr.Header.Get("Content-Type")
	if !assetMIMEAllow[mimeType] {
		httpx.Fail(w, http.StatusUnsupportedMediaType, "bad_mime", "不支持的文件类型: "+mimeType)
		return
	}
	role := r.FormValue("role")
	if role == "" {
		role = "asset"
	}
	key := "templates/" + tplID + "/" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + hdr.Filename
	if _, err := h.Store.Put(key, file); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "storage", "存储失败")
		return
	}
	id, err := h.Repo.AddTemplateAsset(r.Context(), tplID, key, mimeType, role, hdr.Size)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "记录失败")
		return
	}
	h.audit(r, adminID, "template_asset_upload", tplID, map[string]any{"asset": id, "mime": mimeType})
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": id, "storage_key": key})
}
