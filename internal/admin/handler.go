// Package admin serves the super-admin backend: organizer management.
// Auth domain is physically separate from organizer (ARCHITECTURE §2).
package admin

import (
	"net/http"
	"time"

	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/loginguard"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/token"
	"github.com/hertz/captain/internal/turnstile"
	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	Repo  *repo.Repo
	Sig   *token.Signer
	Guard *loginguard.Guard
	TS    *turnstile.Verifier
}

type loginReq struct {
	LoginName string `json:"login_name"`
	Password  string `json:"password"`
	Turnstile string `json:"turnstile_token"`
}

// Login: POST /api/v1/{adminslug}/login — constant 3s delay + 10/10min
// lockout + optional Turnstile (REQ-CHANGE-003).
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
		Kind: token.KindAuth, Role: "admin", Subject: c.ID,
		ExpiresAt: time.Now().Add(12 * time.Hour).Unix(),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"token": tok})
}

func (h *Handler) auth(w http.ResponseWriter, r *http.Request) bool {
	c, err := h.Sig.Verify(httpx.BearerToken(r), token.KindAuth)
	if err != nil || c.Role != "admin" {
		httpx.Fail(w, http.StatusUnauthorized, "unauthorized", "需要超管登录")
		return false
	}
	return true
}

// ListOrganizers: GET /api/v1/admin/organizers
func (h *Handler) ListOrganizers(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
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

// CreateOrganizer: POST /api/v1/admin/organizers
func (h *Handler) CreateOrganizer(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
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
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

type statusReq struct {
	Status string `json:"status"` // active | disabled
}

// SetOrganizerStatus: POST /api/v1/admin/organizers/{id}/status
func (h *Handler) SetOrganizerStatus(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	var req statusReq
	if err := httpx.DecodeJSON(r, &req); err != nil ||
		(req.Status != "active" && req.Status != "disabled") {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "status 必须为 active|disabled")
		return
	}
	if err := h.Repo.SetOrganizerStatus(r.Context(), r.PathValue("id"), req.Status); err != nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动方不存在")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": req.Status})
}
