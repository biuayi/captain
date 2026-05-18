package participation

import (
	"net/http"
	"time"

	"github.com/hertz/captain/internal/audit"
	"github.com/hertz/captain/internal/flow"
	"github.com/hertz/captain/internal/httpx"
)

// Draw: POST /api/v1/p/e/{event_id}/steps/{step_id}/draw — one auditable
// draw per participant; pool-scoped rig→weighted-random; atomic stock;
// grand prize → prize.won (SS-6 big screen) (SS5-05/07/12).
func (h *Handler) Draw(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	stepID := r.PathValue("step_id")
	claims, ok := h.participantAuth(w, r, eventID)
	if !ok {
		return
	}
	pid := claims.Subject
	if !h.RL.Allow(r.Context(), "draw:p:"+pid+":"+eventID, 10, time.Minute) {
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
	if !found || step.Type != flow.StepLottery {
		httpx.Fail(w, http.StatusNotFound, "step_not_found", "抽奖步骤不存在")
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
	if !fl.CanEnter(step.Stage, done) {
		httpx.Fail(w, http.StatusConflict, "stage_gated", "请先完成前序环节")
		return
	}

	emp, _ := h.Repo.ParticipantEmployee(r.Context(), pid)

	// best-effort fast-path lock (the repo advisory xact lock + UNIQUE are
	// the real guarantees; Redis only sheds obvious dup bursts).
	if h.RDB != nil {
		lk := "lot:lock:" + eventID + ":" + stepID + ":" + pid
		if set, _ := h.RDB.SetNX(r.Context(), lk, "1", 10*time.Second).Result(); !set {
			if existing, e := h.Repo.LotteryResultOf(r.Context(), eventID, stepID, pid); e == nil {
				httpx.JSON(w, http.StatusOK, existing)
				return
			}
		}
	}

	res, err := h.Repo.DrawLottery(r.Context(), eventID, stepID, pid, emp)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "抽奖失败")
		return
	}
	_ = h.Repo.AppendAudit(r.Context(), audit.Entry{
		ActorRole: "system", Action: "lottery_draw", Target: eventID,
		Meta: map[string]any{"step": stepID, "participant": pid,
			"pool": res.PoolID, "resolved_by": res.ResolvedBy,
			"prize": res.PrizeCode, "level": res.Level, "repeat": res.Repeat},
		RequestID: httpx.RequestIDOf(r.Context()),
	})
	if !res.Repeat {
		h.completeStage(r.Context(), eventID, partcpnID, step.Stage, fl, done)
	}
	grandPush, _ := step.Config["grandPushToScreen"].(bool)
	if res.Level == "grand" && grandPush && !res.Repeat {
		h.publish(r.Context(), "prize.won", map[string]string{
			"event_id": eventID, "participant_id": pid,
			"prize": res.PrizeName, "prize_code": res.PrizeCode})
	}
	httpx.JSON(w, http.StatusOK, res)
}

// DrawResult: GET /api/v1/p/e/{event_id}/steps/{step_id}/result (SS5-06).
func (h *Handler) DrawResult(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	stepID := r.PathValue("step_id")
	claims, ok := h.participantAuth(w, r, eventID)
	if !ok {
		return
	}
	res, err := h.Repo.LotteryResultOf(r.Context(), eventID, stepID, claims.Subject)
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "no_result", "尚未抽奖")
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}
