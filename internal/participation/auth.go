package participation

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/identity"
	"github.com/hertz/captain/internal/loginguard"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/token"
)

func genJTI() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func revokeKey(jti string) string { return "sess:p:" + jti }

type loginReq struct {
	EmployeeNumber string            `json:"employee_number"`
	PhoneLast4     string            `json:"phone_last4"`
	Name           string            `json:"name"`
	Phone          string            `json:"phone"`
	Company        string            `json:"company"`
	Fingerprint    *identity.Signals `json:"fingerprint"`
	Turnstile      string            `json:"turnstile_token"`
}

// Login: POST /api/v1/p/e/{event_id}/login — whitelist factor auth → JWT
// (DESIGN §SS-2, SS2-07). Required factors: employee_number + phone_last4
// (+ company when multi-company); optional name/phone per event flags.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	if !h.RL.Allow(r.Context(), "plogin:ip:"+httpx.ClientIP(r), 60, time.Minute) {
		httpx.Fail(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	var req loginReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	ctx := r.Context()
	ev, err := h.Repo.Event(ctx, eventID)
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "event_not_found", "活动不存在")
		return
	}
	if ev.Status != "active" || time.Now().After(ev.EndAt) {
		httpx.Fail(w, http.StatusForbidden, "event_inactive", "活动未开放")
		return
	}
	idc, err := h.Repo.EventIdentity(ctx, eventID)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "event config")
		return
	}

	scope := "participant:" + eventID
	loginguard.Wait(ctx)
	if h.Guard.Locked(ctx, scope, req.EmployeeNumber) {
		httpx.Fail(w, http.StatusLocked, "account_locked", "尝试过多，请稍后再试")
		return
	}
	if !h.TS.Verify(ctx, req.Turnstile, httpx.ClientIP(r)) {
		httpx.Fail(w, http.StatusForbidden, "captcha_failed", "人机验证未通过")
		return
	}

	emp := strings.TrimSpace(req.EmployeeNumber)
	if emp == "" || strings.TrimSpace(req.PhoneLast4) == "" {
		httpx.Fail(w, http.StatusBadRequest, "missing_factor", "工号与手机后4位必填")
		return
	}
	companyNorm := ""
	if idc.MultiCompany {
		companyNorm = identity.CompanyNorm(req.Company)
		if companyNorm == "" {
			httpx.Fail(w, http.StatusBadRequest, "missing_factor", "本活动需填写企业名称")
			return
		}
	}
	fail := func() {
		h.Guard.RecordFailure(ctx, scope, emp)
		httpx.Fail(w, http.StatusUnauthorized, "bad_credentials", "登录失败")
	}

	e, err := h.Repo.MatchWhitelistLogin(ctx, eventID, emp, companyNorm)
	if err != nil {
		fail()
		return
	}
	if strings.TrimSpace(req.PhoneLast4) != e.PhoneLast4 {
		fail()
		return
	}
	if idc.RequireName && strings.TrimSpace(req.Name) != strings.TrimSpace(e.Name) {
		fail()
		return
	}
	if idc.RequirePhone && strings.TrimSpace(req.Phone) != strings.TrimSpace(e.PhoneNumber) {
		fail()
		return
	}
	if e.Status == "blocked" {
		httpx.Fail(w, http.StatusForbidden, "entry_blocked", "该名单条目已被禁用")
		return
	}

	// fingerprint (optional but recommended; anti-share signal, not the key)
	fp := ""
	if req.Fingerprint != nil {
		if payload, perr := identity.Normalize(*req.Fingerprint); perr == nil {
			fp = identity.Hash(h.Pepper, payload)
		}
	}

	pkey := identity.ParticipantKey(h.Pepper, "staff", eventID, e.ID)
	entryID := e.ID
	pid, _, uerr := h.Repo.UpsertParticipantFull(ctx, repo.ParticipantUpsert{
		EventID: eventID, ParticipantKey: pkey, IdentityType: "staff_whitelist",
		ParticipantType: "staff", FingerprintHash: fp, WhitelistEntryID: &entryID,
	})
	if uerr != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "identity")
		return
	}

	jti := genJTI()
	if e.Status == "unused" {
		won, cerr := h.Repo.ClaimWhitelistWithJTI(ctx, e.ID, pid, fp, jti)
		if cerr != nil {
			httpx.Fail(w, http.StatusInternalServerError, "db", "claim")
			return
		}
		if !won {
			// lost the race → fall through to the claimed-handling below
			e2, _ := h.Repo.MatchWhitelistLogin(ctx, eventID, emp, companyNorm)
			e = e2
		}
	}
	if e.Status == "claimed" || e.ClaimedFP != "" {
		if fp != "" && e.ClaimedFP != "" && fp != e.ClaimedFP {
			pidStr := pid
			_ = h.Repo.AddWarning(ctx, &pidStr, eventID, "fingerprint_mismatch",
				map[string]any{"employee_number": emp})
			if idc.StrictFingerprint {
				httpx.Fail(w, http.StatusForbidden, "fingerprint_blocked",
					"设备校验未通过，请联系活动方解绑")
				return
			}
		}
		// re-login (same identity, possibly new device): rotate session jti,
		// invalidating any previous session (顶号防借号).
		if serr := h.Repo.SetWhitelistJTI(ctx, e.ID, jti); serr != nil {
			httpx.Fail(w, http.StatusInternalServerError, "db", "session")
			return
		}
	}

	exp := time.Now().Add(8 * time.Hour)
	if hard := ev.EndAt.Add(2 * time.Hour); hard.Before(exp) {
		exp = hard
	}
	tok, terr := h.Sig.Sign(token.Claims{
		Kind: token.KindAuth, Role: token.RoleParticipant, Subject: pid,
		EventID: eventID, JTI: jti, ExpiresAt: exp.Unix(),
	})
	if terr != nil {
		httpx.Fail(w, http.StatusInternalServerError, "internal", "sign")
		return
	}
	h.Guard.Reset(ctx, scope, emp)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"token":       tok,
		"participant": map[string]string{"id": pid, "name": e.Name},
	})
}

// participantAuth verifies a participant JWT for eventID: signature/role,
// event scope, jti not revoked, and jti == whitelist active jti (顶号).
func (h *Handler) participantAuth(w http.ResponseWriter, r *http.Request, eventID string) (token.Claims, bool) {
	c, ok := httpx.AuthClaims(r, h.Sig, token.RoleParticipant)
	if !ok || c.EventID != eventID {
		httpx.Fail(w, http.StatusUnauthorized, "no_session", "请先登录")
		return token.Claims{}, false
	}
	if h.RDB != nil && c.JTI != "" {
		if n, _ := h.RDB.Exists(r.Context(), revokeKey(c.JTI)).Result(); n == 1 {
			httpx.Fail(w, http.StatusUnauthorized, "session_revoked", "会话已失效，请重新登录")
			return token.Claims{}, false
		}
	}
	active, err := h.Repo.ParticipantActiveJTI(r.Context(), c.Subject)
	if err == nil && active != "" && active != c.JTI {
		httpx.Fail(w, http.StatusUnauthorized, "session_superseded", "账号已在其他设备登录")
		return token.Claims{}, false
	}
	return c, true
}

// Logout: POST /api/v1/p/e/{event_id}/logout — revoke the current jti.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	c, ok := h.participantAuth(w, r, eventID)
	if !ok {
		return
	}
	if h.RDB != nil && c.JTI != "" {
		ttl := max(time.Until(time.Unix(c.ExpiresAt, 0)), time.Minute)
		h.RDB.Set(r.Context(), revokeKey(c.JTI), "1", ttl)
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Me: GET /api/v1/p/e/{event_id}/me — current participant (progress added
// in SS-4).
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	c, ok := h.participantAuth(w, r, eventID)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"participant_id": c.Subject,
		"event_id":       eventID,
	})
}
