// Package organizer serves the authenticated event-owner backend.
// Every handler is scoped to the logged-in organizer (multi-tenant, §2).
package organizer

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hertz/captain/internal/export"
	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/identity"
	"github.com/hertz/captain/internal/loginguard"
	"github.com/hertz/captain/internal/realtime"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/storage"
	"github.com/hertz/captain/internal/token"
	"github.com/hertz/captain/internal/turnstile"
	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	Repo    *repo.Repo
	Sig     *token.Signer
	RT      *realtime.Manager
	Export  *export.Worker
	Store   storage.Storage
	BaseURL string
	Guard   *loginguard.Guard
	TS      *turnstile.Verifier
}

type loginReq struct {
	LoginName string `json:"login_name"`
	Password  string `json:"password"`
	Turnstile string `json:"turnstile_token"`
}

// Login: POST /api/v1/org/login — authorized organizers only.
// Constant 3s delay + 10-fail/10min lockout + optional Turnstile (REQ-CHANGE-003).
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	ctx := r.Context()
	loginguard.Wait(ctx) // 恒定 ~3s，无论结果
	const scope = "organizer"
	if h.Guard.Locked(ctx, scope, req.LoginName) {
		httpx.Fail(w, http.StatusLocked, "account_locked", "账号已锁定，请稍后再试")
		return
	}
	if !h.TS.Verify(ctx, req.Turnstile, httpx.ClientIP(r)) {
		httpx.Fail(w, http.StatusForbidden, "captcha_failed", "人机验证未通过")
		return
	}
	c, err := h.Repo.OrganizerByLogin(ctx, req.LoginName)
	if err != nil || c.Status != "active" ||
		bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(req.Password)) != nil {
		h.Guard.RecordFailure(ctx, scope, req.LoginName)
		httpx.Fail(w, http.StatusUnauthorized, "bad_credentials", "登录失败")
		return
	}
	h.Guard.Reset(ctx, scope, req.LoginName)
	tok, _ := h.Sig.Sign(token.Claims{
		Kind: token.KindAuth, Role: "organizer", Subject: c.ID,
		ExpiresAt: time.Now().Add(12 * time.Hour).Unix(),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"token": tok, "organizer": map[string]string{"id": c.ID, "name": c.Name}})
}

// auth returns the organizer id from the bearer token.
func (h *Handler) auth(w http.ResponseWriter, r *http.Request) (string, bool) {
	c, err := h.Sig.Verify(httpx.BearerToken(r), token.KindAuth)
	if err != nil || c.Role != "organizer" {
		httpx.Fail(w, http.StatusUnauthorized, "unauthorized", "需要活动方登录")
		return "", false
	}
	return c.Subject, true
}

// Events: GET /api/v1/org/events
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	evs, err := h.Repo.EventsByOrganizer(r.Context(), orgID)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "query failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"events": evs})
}

// Event detail with live attendance: GET /api/v1/org/events/{id}
func (h *Handler) Event(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	ev, err := h.Repo.Event(r.Context(), r.PathValue("id"))
	if err != nil || ev.OrganizerID != orgID {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动不存在")
		return
	}
	snap := h.RT.Snapshot(r.Context(), ev.ID)
	httpx.JSON(w, http.StatusOK, map[string]any{"event": ev, "checkin_count": snap.Count})
}

// Participants: GET /api/v1/org/events/{id}/participants
func (h *Handler) Participants(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	ev, err := h.Repo.Event(r.Context(), r.PathValue("id"))
	if err != nil || ev.OrganizerID != orgID {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动不存在")
		return
	}
	rows, err := h.Repo.ListParticipants(r.Context(), ev.ID)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "query failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"participants": rows, "total": len(rows)})
}

// EntryLink: GET /api/v1/org/events/{id}/entry
// Returns the static QR payload (event_token) + participant/screen URLs so
// the organizer can print the venue QR code (ARCHITECTURE §3).
func (h *Handler) EntryLink(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	ev, err := h.Repo.Event(r.Context(), r.PathValue("id"))
	if err != nil || ev.OrganizerID != orgID {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动不存在")
		return
	}
	et, err := h.Sig.Sign(token.Claims{
		Kind: token.KindEvent, EventID: ev.ID,
		NotBefore: time.Now().Add(-time.Hour).Unix(),
		ExpiresAt: ev.EndAt.Add(24 * time.Hour).Unix(),
	})
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "internal", "sign failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{
		"event_id":    ev.ID,
		"event_token": et,
		"mobile_url":  h.BaseURL + "/m/" + ev.ID + "?et=" + et,
		"screen_url":  h.BaseURL + "/screen/" + ev.ID,
	})
}

// ImportWhitelist: POST /api/v1/org/events/{id}/whitelist/import
// Body = CSV text with header: employee_number,name,phone (REQ-CHANGE-001).
func (h *Handler) ImportWhitelist(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	ev, err := h.Repo.Event(r.Context(), r.PathValue("id"))
	if err != nil || ev.OrganizerID != orgID {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动不存在")
		return
	}
	defer r.Body.Close()
	cr := csv.NewReader(io.LimitReader(r.Body, 4<<20))
	cr.FieldsPerRecord = -1
	recs, err := cr.ReadAll()
	if err != nil || len(recs) < 2 {
		httpx.Fail(w, http.StatusBadRequest, "bad_csv", "CSV 需含表头 employee_number,name,phone 且至少一行数据")
		return
	}
	var rows []repo.WLImportRow
	for _, rec := range recs[1:] {
		if len(rec) < 3 {
			continue
		}
		emp := strings.TrimSpace(rec[0])
		name := strings.TrimSpace(rec[1])
		phone := strings.TrimSpace(rec[2])
		if emp == "" || name == "" || phone == "" {
			continue
		}
		rows = append(rows, repo.WLImportRow{
			EmployeeNumber: emp, Name: name, Phone: phone,
			PhoneLast4: identity.Last4(phone),
		})
	}
	batch := fmt.Sprintf("imp-%d", time.Now().Unix())
	n, err := h.Repo.InsertWhitelist(r.Context(), ev.ID, orgID, batch, rows)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "导入失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"parsed": len(rows), "inserted": n, "batch": batch})
}

// ListWhitelist: GET /api/v1/org/events/{id}/whitelist
func (h *Handler) ListWhitelist(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	ev, err := h.Repo.Event(r.Context(), r.PathValue("id"))
	if err != nil || ev.OrganizerID != orgID {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动不存在")
		return
	}
	rows, err := h.Repo.ListWhitelist(r.Context(), ev.ID, orgID)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "查询失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"whitelist": rows, "total": len(rows)})
}

// CreateExport: POST /api/v1/org/events/{id}/export — async (§4.2/§6).
func (h *Handler) CreateExport(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	ev, err := h.Repo.Event(r.Context(), r.PathValue("id"))
	if err != nil || ev.OrganizerID != orgID {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动不存在")
		return
	}
	jobID, err := h.Repo.CreateExportJob(r.Context(), orgID, ev.ID)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "create job failed")
		return
	}
	if err := h.Export.Request(r.Context(), jobID); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "mq", "enqueue failed")
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "pending"})
}

// ExportStatus: GET /api/v1/org/exports/{job_id}
func (h *Handler) ExportStatus(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	job, err := h.Repo.ExportJob(r.Context(), r.PathValue("job_id"), orgID)
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "任务不存在")
		return
	}
	httpx.JSON(w, http.StatusOK, job)
}

// ExportDownload: GET /api/v1/org/exports/{job_id}/download
func (h *Handler) ExportDownload(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	job, err := h.Repo.ExportJob(r.Context(), r.PathValue("job_id"), orgID)
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "任务不存在")
		return
	}
	if job.Status != "done" || job.StorageKey == "" {
		httpx.Fail(w, http.StatusConflict, "not_ready", "导出尚未完成")
		return
	}
	rc, err := h.Store.Open(job.StorageKey)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "storage", "读取失败")
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="export-`+job.ID+`.csv"`)
	_, _ = io.Copy(w, rc)
}
