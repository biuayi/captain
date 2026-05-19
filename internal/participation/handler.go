// Package participation serves the user side: event landing, strong-identity
// whitelist login (participant JWT), and D5-gated step submission for the
// R1-R4 flow (see docs/DESIGN.md §SS-2/§SS-4).
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
	"github.com/hertz/captain/internal/loginguard"
	"github.com/hertz/captain/internal/realtime"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/storage"
	"github.com/hertz/captain/internal/token"
	"github.com/hertz/captain/internal/turnstile"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/skip2/go-qrcode"
)

type Handler struct {
	Repo       *repo.Repo
	Sig        *token.Signer
	RT         *realtime.Manager
	RL         *httpx.RateLimiter
	JS         jetstream.JetStream
	Pepper     string // REQ-CHANGE-001 fingerprint/participant_key HMAC pepper
	TS         *turnstile.Verifier
	Guard      *loginguard.Guard // participant login hardening (SS2-06)
	RDB        *redis.Client     // participant session jti revocation (SS2-08/09)
	Store      storage.Storage   // R2 uploads → object storage (SS4-06)
	OpenLegacy bool              // CAPTAIN_OPEN_PARTICIPATION: keep anon/device path (SS2-16)
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

	raw, err := h.Repo.FlowSchema(r.Context(), ev.FlowConfigID)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "flow_missing", "flow not found")
		return
	}

	// Legacy anon/device-session path, only when explicitly enabled
	// (CAPTAIN_OPEN_PARTICIPATION, SS2-16). Default model: strong-identity
	// login (SS2-11) — no cookie minted here, client must POST /login.
	if h.OpenLegacy {
		if deviceUUID := r.URL.Query().Get("d"); deviceUUID != "" {
			dh := deviceHash(deviceUUID)
			if !h.RL.Allow(r.Context(), "mint:dev:"+dh, 10, time.Minute) {
				httpx.Fail(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
				return
			}
			exp := ev.EndAt.Add(2 * time.Hour)
			if cap8 := time.Now().Add(8 * time.Hour); exp.After(cap8) {
				exp = cap8
			}
			sess, serr := h.Sig.Sign(token.Claims{
				Kind: token.KindSession, EventID: eventID, DeviceHash: dh,
				ExpiresAt: exp.Unix(),
			})
			if serr != nil {
				httpx.Fail(w, http.StatusInternalServerError, "internal", "sign failed")
				return
			}
			secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
			httpx.SetSessionCookie(w, sess, exp, secure)
		}
	}

	// F2-01: include identity-factor flags so the mobile frontend knows which
	// login fields to render. On error (e.g. misconfigured event), omit the
	// field (don't fail landing). Decision: expose on landing, not /p/config.
	resp := map[string]any{
		"event":      ev,
		"flow":       json.RawMessage(raw),
		"need_login": !h.OpenLegacy,
	}
	if idc, err := h.Repo.EventIdentity(r.Context(), eventID); err == nil {
		resp["identity"] = map[string]any{
			"require_name":  idc.RequireName,
			"require_phone": idc.RequirePhone,
			"multi_company": idc.MultiCompany,
		}
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// submitReq is the step submission body (SS-4; identity now via JWT).
type submitReq struct {
	Fields    map[string]any   `json:"fields"`          // form (R2)
	Answers   map[string][]int `json:"answers"`         // exam (R3): qIdx -> picked option idxs
	Answer    *int             `json:"answer"`          // game (legacy)
	Pledge    bool             `json:"pledge"`          // charity
	DeviceID  string           `json:"device_id"`       // client device id (G2; export-visible)
	Turnstile string           `json:"turnstile_token"` // checkin captcha
	Geo       *struct {
		Lat      float64 `json:"lat"`
		Lng      float64 `json:"lng"`
		Accuracy float64 `json:"accuracy"`
	} `json:"geo"` // checkin location (optional)
}

// nextEnabledStage returns the enabled stage after `stage`, or "" if none.
func nextEnabledStage(fl *flow.Flow, stage string) string {
	en := fl.EnabledStages()
	for i, s := range en {
		if s == stage && i+1 < len(en) {
			return en[i+1]
		}
	}
	return ""
}

// completeStage marks a stage done, advances current_stage, triggers the
// participated count on the first completed stage, and completes the
// participation when all enabled stages are done (D5, SS4-04/10/11/12).
func (h *Handler) completeStage(ctx context.Context, eventID, partcpnID, stage string, fl *flow.Flow, done map[string]bool) {
	if stage == "" || done[stage] {
		return
	}
	first := len(done) == 0
	next := nextEnabledStage(fl, stage)
	_ = h.Repo.SetStageDone(ctx, partcpnID, stage, next)
	done[stage] = true
	if first {
		h.RT.OnParticipated(ctx, eventID) // D5 participated count (SS-6)
		h.publish(ctx, "checkin.submitted", map[string]string{
			"event_id": eventID, "participation_id": partcpnID})
	}
	allDone := true
	for _, s := range fl.EnabledStages() {
		if !done[s] {
			allDone = false
			break
		}
	}
	if allDone {
		_ = h.Repo.MarkCompletedAll(ctx, partcpnID)
	}
}

// Submit: POST /api/v1/p/e/{event_id}/steps/{step_id}/submit
// Participant-JWT auth + D5 sequential stage gating (SS4-01/05..11).
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	stepID := r.PathValue("step_id")
	claims, ok := h.participantAuth(w, r, eventID)
	if !ok {
		return
	}
	pid := claims.Subject
	if !h.RL.Allow(r.Context(), "submit:p:"+pid+":"+eventID, 30, time.Minute) ||
		!h.RL.Allow(r.Context(), "submit:ip:"+httpx.ClientIP(r), 120, time.Minute) {
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
	partcpnID, err := h.Repo.EnsureParticipation(r.Context(), eventID, pid)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "participation failed")
		return
	}
	_, done, _, _ := h.Repo.StageState(r.Context(), eventID, pid)
	if done == nil {
		done = map[string]bool{}
	}
	// D5 sequential gating (SS4-05)
	if !fl.CanEnter(step.Stage, done) {
		httpx.Fail(w, http.StatusConflict, "stage_gated", "请先完成前序环节")
		return
	}

	var body submitReq
	_ = httpx.DecodeJSON(r, &body)
	if body.DeviceID != "" { // G2: capture device id (export-visible record field)
		_ = h.Repo.SetDataFields(r.Context(), partcpnID, "", "", body.DeviceID)
	}
	resp := map[string]any{"stepId": stepID, "nextStepId": step.NextStepID}

	switch step.Type {
	case flow.StepCheckin: // R1 multi-day
		if h.TS != nil && !h.TS.Verify(r.Context(), body.Turnstile, httpx.ClientIP(r)) {
			httpx.Fail(w, http.StatusForbidden, "captcha_failed", "人机验证未通过")
			return
		}
		idc, _ := h.Repo.EventIdentity(r.Context(), eventID)
		loc, lerr := time.LoadLocation(idc.Timezone)
		if lerr != nil {
			loc = time.UTC
		}
		day := time.Now().In(loc).Format("2006-01-02")
		var lat, lng, acc *float64
		if body.Geo != nil {
			lat, lng, acc = &body.Geo.Lat, &body.Geo.Lng, &body.Geo.Accuracy
		}
		if _, err := h.Repo.MarkCheckinDay(r.Context(), partcpnID, eventID, day, lat, lng, acc); err != nil {
			httpx.Fail(w, http.StatusInternalServerError, "db", "checkin failed")
			return
		}
		_, _ = h.Repo.MarkCheckin(r.Context(), partcpnID) // keep legacy checkin_at first-time
		days := 1
		if d, ok := step.Config["days"].(float64); ok && d >= 1 {
			days = int(d)
		}
		got, _ := h.Repo.DistinctCheckinDays(r.Context(), partcpnID)
		_ = h.Repo.RecordStep(r.Context(), partcpnID, stepID, step.Type,
			map[string]any{"day": day, "days_done": got, "days_required": days})
		resp["days_done"], resp["days_required"] = got, days
		if got >= days {
			h.completeStage(r.Context(), eventID, partcpnID, step.Stage, fl, done)
			resp["stage_complete"] = true
		}

	case flow.StepForm: // R2 survey
		_ = h.Repo.RecordStep(r.Context(), partcpnID, stepID, step.Type, body.Fields)
		f1, f2 := summarizeForm(body.Fields)
		_ = h.Repo.SetDataFields(r.Context(), partcpnID, f1, f2, "")
		resp["saved"] = true
		h.completeStage(r.Context(), eventID, partcpnID, step.Stage, fl, done)

	case flow.StepExam: // R3 scored
		score, total, passed := h.scoreExam(r.Context(), eventID, stepID, step, body.Answers)
		_ = h.Repo.RecordStep(r.Context(), partcpnID, stepID, step.Type,
			map[string]any{"answers": body.Answers, "score": score, "total": total, "passed": passed})
		resp["score"], resp["total"], resp["passed"] = score, total, passed
		h.completeStage(r.Context(), eventID, partcpnID, step.Stage, fl, done)

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

	case flow.StepLottery:
		// draw is a dedicated endpoint (SS-5); submit here is a no-op marker.
		resp["use"] = "POST .../draw"

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
