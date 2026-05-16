// Package participation serves the anonymous user side: event entry,
// device-session minting, and step submission with idempotent checkin
// (ARCHITECTURE §3/§4/§5).
package participation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/hertz/captain/internal/flow"
	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/identity"
	"github.com/hertz/captain/internal/realtime"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/token"
	"github.com/hertz/captain/internal/turnstile"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/skip2/go-qrcode"
)

type Handler struct {
	Repo   *repo.Repo
	Sig    *token.Signer
	RT     *realtime.Manager
	RL     *httpx.RateLimiter
	JS     jetstream.JetStream
	Pepper string // REQ-CHANGE-001 fingerprint/participant_key HMAC pepper
	TS     *turnstile.Verifier
}

func deviceHash(uuid string) string {
	s := sha256.Sum256([]byte(uuid))
	return hex.EncodeToString(s[:])[:32]
}

// Bootstrap: GET /api/v1/p/e/{event_id}?et=<event_token>&d=<device_uuid>
// Verifies the static event_token, mints a device-session cookie, returns flow.
func (h *Handler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	if !h.RL.Allow(r.Context(), "entry:ip:"+httpx.ClientIP(r), 120, time.Minute) {
		httpx.Fail(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	et := r.URL.Query().Get("et")
	claims, err := h.Sig.Verify(et, token.KindEvent)
	if err != nil || claims.EventID != eventID {
		httpx.Fail(w, http.StatusUnauthorized, "bad_event_token", "invalid event token")
		return
	}
	ev, err := h.Repo.Event(r.Context(), eventID)
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "event_not_found", "event not found")
		return
	}
	if ev.Status != "active" || time.Now().After(ev.EndAt) {
		httpx.Fail(w, http.StatusForbidden, "event_inactive", "event is not active")
		return
	}

	deviceUUID := r.URL.Query().Get("d")
	if deviceUUID == "" {
		httpx.Fail(w, http.StatusBadRequest, "missing_device", "device id required")
		return
	}
	dh := deviceHash(deviceUUID)
	// key mint limiter by hashed device so raw d= rotation can't bypass (codex review)
	if !h.RL.Allow(r.Context(), "mint:dev:"+dh, 10, time.Minute) {
		httpx.Fail(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	// session hard-expires at event_end+2h, capped to 8h absolute
	exp := ev.EndAt.Add(2 * time.Hour)
	if cap8 := time.Now().Add(8 * time.Hour); exp.After(cap8) {
		exp = cap8
	}
	sess, err := h.Sig.Sign(token.Claims{
		Kind: token.KindSession, EventID: eventID, DeviceHash: dh,
		ExpiresAt: exp.Unix(),
	})
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "internal", "sign failed")
		return
	}
	httpx.SetSessionCookie(w, sess, exp, r.TLS != nil)

	raw, err := h.Repo.FlowSchema(r.Context(), ev.FlowConfigID)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "flow_missing", "flow not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"event": ev,
		"flow":  json.RawMessage(raw),
	})
}

// session resolves the device-session cookie for a given event.
func (h *Handler) session(w http.ResponseWriter, r *http.Request, eventID string) (token.Claims, bool) {
	c, err := h.Sig.Verify(httpx.SessionToken(r), token.KindSession)
	if err != nil || c.EventID != eventID {
		httpx.Fail(w, http.StatusUnauthorized, "no_session", "no valid session")
		return token.Claims{}, false
	}
	return c, true
}

type submitReq struct {
	Fields map[string]any `json:"fields"` // form
	Answer *int           `json:"answer"` // game
	Pledge bool           `json:"pledge"` // charity

	// REQ-CHANGE-001 (optional; absent => legacy device-session identity,
	// keeps the existing demo / load path working — additive integration).
	ParticipantType string            `json:"participant_type"` // staff | external
	Fingerprint     *identity.Signals `json:"fingerprint"`
	Name            string            `json:"name"`
	EmployeeNumber  string            `json:"employee_number"`
	PhoneLast4      string            `json:"phone_last4"`
	Turnstile       string            `json:"turnstile_token"` // REQ-CHANGE-004
}

// resolveIdentity implements the staff/external branches (codex algorithm).
// Returns participantID, participationID, or an error code string.
func (h *Handler) resolveIdentity(ctx context.Context, eventID string, b submitReq) (pid, partcpn, code string) {
	payload, err := identity.Normalize(*b.Fingerprint)
	if err != nil {
		return "", "", identity.ErrBadFingerprint
	}
	fp := identity.Hash(h.Pepper, payload)

	if b.ParticipantType == "staff" {
		emp := strings.TrimSpace(b.EmployeeNumber)
		e, err := h.Repo.WhitelistByEmployee(ctx, eventID, emp)
		if err != nil || strings.TrimSpace(e.Name) != strings.TrimSpace(b.Name) {
			return "", "", identity.ErrStaffNotInWhitelist
		}
		if e.Status == "blocked" {
			return "", "", identity.ErrEntryBlocked
		}
		if e.Status == "claimed" && e.ClaimedFP != fp {
			return "", "", identity.ErrEntryClaimedElsewhere
		}
		if e.Status == "unused" && strings.TrimSpace(b.PhoneLast4) != e.PhoneLast4 {
			return "", "", identity.ErrPhoneMismatch
		}
		key := identity.ParticipantKey(h.Pepper, "staff", eventID, e.ID)
		entryID := e.ID
		pid, _, err = h.Repo.UpsertParticipantFull(ctx, repo.ParticipantUpsert{
			EventID: eventID, ParticipantKey: key, IdentityType: "staff_whitelist",
			ParticipantType: "staff", FingerprintHash: fp, WhitelistEntryID: &entryID,
		})
		if err != nil {
			return "", "", "db"
		}
		if e.Status == "unused" {
			won, err := h.Repo.ClaimWhitelist(ctx, e.ID, pid, fp)
			if err != nil {
				return "", "", "db"
			}
			if !won {
				st, cpid, cfp, _ := h.Repo.WhitelistClaimState(ctx, e.ID)
				if st == "blocked" {
					return "", "", identity.ErrEntryBlocked
				}
				if !(cfp == fp && cpid == pid) {
					return "", "", identity.ErrEntryClaimedElsewhere
				}
			}
		}
	} else { // external
		key := identity.ParticipantKey(h.Pepper, "external", eventID, fp)
		var err error
		pid, _, err = h.Repo.UpsertParticipantFull(ctx, repo.ParticipantUpsert{
			EventID: eventID, ParticipantKey: key, IdentityType: "external_fingerprint",
			ParticipantType: "external", FingerprintHash: fp,
		})
		if err != nil {
			return "", "", "db"
		}
	}
	partcpn, err = h.Repo.EnsureParticipation(ctx, eventID, pid)
	if err != nil {
		return "", "", "db"
	}
	return pid, partcpn, ""
}

// Submit: POST /api/v1/p/e/{event_id}/steps/{step_id}/submit
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	stepID := r.PathValue("step_id")
	sess, ok := h.session(w, r, eventID)
	if !ok {
		return
	}
	if !h.RL.Allow(r.Context(), "submit:dev:"+sess.DeviceHash+":"+eventID, 6, time.Minute) ||
		!h.RL.Allow(r.Context(), "submit:ip:"+httpx.ClientIP(r), 60, time.Minute) {
		httpx.Fail(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}

	ev, err := h.Repo.Event(r.Context(), eventID)
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "event_not_found", "event not found")
		return
	}
	raw, err := h.Repo.FlowSchema(r.Context(), ev.FlowConfigID)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "flow_missing", "flow not found")
		return
	}
	fl, err := flow.Parse(raw)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "flow_invalid", err.Error())
		return
	}
	step, found := fl.Step(stepID)
	if !found {
		httpx.Fail(w, http.StatusNotFound, "step_not_found", "step not found")
		return
	}

	var body submitReq
	_ = httpx.DecodeJSON(r, &body) // body optional for some steps

	var partID, partcpnID string
	if (body.ParticipantType == "staff" || body.ParticipantType == "external") && body.Fingerprint != nil {
		// REQ-CHANGE-001 identity (fingerprint + whitelist)
		var code string
		partID, partcpnID, code = h.resolveIdentity(r.Context(), eventID, body)
		switch code {
		case "":
		case "db":
			httpx.Fail(w, http.StatusInternalServerError, "db", "identity resolve failed")
			return
		case identity.ErrBadFingerprint:
			httpx.Fail(w, http.StatusBadRequest, code, "指纹信息不完整")
			return
		default: // STAFF_NOT_IN_WHITELIST / PHONE_MISMATCH / ENTRY_BLOCKED / ENTRY_CLAIMED_ELSEWHERE
			httpx.Fail(w, http.StatusForbidden, code, "白名单校验未通过")
			return
		}
	} else {
		// legacy device-session identity (keeps existing demo / load path)
		pkey := sess.DeviceHash
		idType, idVal := "anon", ""
		if step.Type == flow.StepForm {
			if v, ok := body.Fields["phone"].(string); ok && v != "" {
				idType, idVal = "phone", v
			}
		}
		var err error
		partID, _, err = h.Repo.UpsertParticipant(r.Context(), eventID, pkey, idType, idVal)
		if err != nil {
			httpx.Fail(w, http.StatusInternalServerError, "db", "participant upsert failed")
			return
		}
		partcpnID, err = h.Repo.EnsureParticipation(r.Context(), eventID, partID)
		if err != nil {
			httpx.Fail(w, http.StatusInternalServerError, "db", "participation failed")
			return
		}
	}

	resp := map[string]any{"stepId": stepID, "nextStepId": step.NextStepID}

	switch step.Type {
	case flow.StepCheckin:
		// 用户签到加入 Cloudflare 人机认证（REQ-CHANGE-004；mode=off 时放行）
		if h.TS != nil && !h.TS.Verify(r.Context(), body.Turnstile, httpx.ClientIP(r)) {
			httpx.Fail(w, http.StatusForbidden, "captcha_failed", "人机验证未通过")
			return
		}
		first, err := h.Repo.MarkCheckin(r.Context(), partcpnID)
		if err != nil {
			httpx.Fail(w, http.StatusInternalServerError, "db", "checkin failed")
			return
		}
		if first {
			h.RT.OnCheckin(r.Context(), eventID)
			h.publish(r.Context(), "checkin.submitted", map[string]string{
				"event_id": eventID, "participation_id": partcpnID})
		}
		resp["checked_in"] = true

	case flow.StepForm:
		_ = h.Repo.RecordStep(r.Context(), partcpnID, stepID, step.Type, body.Fields)
		resp["saved"] = true

	case flow.StepGame:
		correct := false
		if ci, ok := step.Config["correctOptionIndex"].(float64); ok && body.Answer != nil {
			correct = int(ci) == *body.Answer
		}
		_ = h.Repo.RecordStep(r.Context(), partcpnID, stepID, step.Type,
			map[string]any{"answer": body.Answer, "correct": correct})
		resp["correct"] = correct

	case flow.StepCharity:
		_ = h.Repo.RecordStep(r.Context(), partcpnID, stepID, step.Type,
			map[string]any{"pledge": body.Pledge})
		resp["thanks"] = true

	case flow.StepReward:
		_ = h.Repo.RecordStep(r.Context(), partcpnID, stepID, step.Type,
			map[string]any{"viewed": true})
		resp["reward"] = step.Config

	case flow.StepResult:
		_ = h.Repo.RecordStep(r.Context(), partcpnID, stepID, step.Type, map[string]any{})
	}

	status := "in_progress"
	if step.NextStepID == nil {
		status = "completed"
	}
	_ = h.Repo.SetLastStep(r.Context(), partcpnID, stepID, status)
	h.publish(r.Context(), "participant.step_completed", map[string]string{
		"event_id": eventID, "participation_id": partcpnID,
		"step_id": stepID, "step_type": step.Type})

	httpx.JSON(w, http.StatusOK, resp)
}

// Count: GET /api/v1/p/e/{event_id}/count
func (h *Handler) Count(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, h.RT.Snapshot(r.Context(), r.PathValue("event_id")))
}

// QR: GET /api/v1/p/e/{event_id}/qr — PNG QR of the participant entry URL
// (scheme/host derived from the request so it matches the public domain;
// REQ-CHANGE: big screen / organizer shows a scannable check-in QR).
func (h *Handler) QR(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	ev, err := h.Repo.Event(r.Context(), eventID)
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "event_not_found", "event not found")
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
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	link := scheme + "://" + host + "/m/" + ev.ID + "?et=" + et
	png, err := qrcode.Encode(link, qrcode.Medium, 512)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "qr", "qr failed")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

// Info: GET /api/v1/p/e/{event_id}/info — public event meta for the big
// screen (name + expected_count drive the progress target).
func (h *Handler) Info(w http.ResponseWriter, r *http.Request) {
	ev, err := h.Repo.Event(r.Context(), r.PathValue("event_id"))
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "event_not_found", "event not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"event_id":       ev.ID,
		"name":           ev.Name,
		"expected_count": ev.ExpectedCount,
	})
}

// Stream: GET /api/v1/p/e/{event_id}/stream  (SSE, public — for the big screen)
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	ch, cancel := h.RT.Subscribe(r.Context(), eventID)
	defer cancel()
	httpx.ServeSSE(w, r, ch)
}

func (h *Handler) publish(ctx context.Context, subject string, v any) {
	if h.JS == nil {
		return
	}
	b, _ := json.Marshal(v)
	_, _ = h.JS.Publish(ctx, subject, b)
}
