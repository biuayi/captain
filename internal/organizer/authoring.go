package organizer

import (
	"encoding/csv"
	"io"
	"net/http"
	"strings"

	"github.com/hertz/captain/internal/audit"
	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/repo"
)

// ownedEvent resolves the path event and enforces tenant ownership.
func (h *Handler) ownedEvent(w http.ResponseWriter, r *http.Request, orgID string) (string, bool) {
	ev, err := h.Repo.Event(r.Context(), r.PathValue("id"))
	if err != nil || ev.OrganizerID != orgID {
		httpx.Fail(w, http.StatusNotFound, "not_found", "活动不存在")
		return "", false
	}
	return ev.ID, true
}

func (h *Handler) orgAudit(r *http.Request, orgID, action, target string, meta map[string]any) {
	_ = h.Repo.AppendAudit(r.Context(), audit.Entry{
		ActorRole: "organizer", ActorID: orgID, Action: action, Target: target,
		Meta: meta, RequestID: httpx.RequestIDOf(r.Context()),
	})
}

func readCSV(r *http.Request) ([][]string, map[string]int, error) {
	defer r.Body.Close()
	cr := csv.NewReader(io.LimitReader(r.Body, 8<<20))
	cr.FieldsPerRecord = -1
	recs, err := cr.ReadAll()
	if err != nil || len(recs) < 2 {
		return nil, nil, err
	}
	col := map[string]int{}
	for i, c := range recs[0] {
		col[strings.ToLower(strings.TrimSpace(c))] = i
	}
	return recs, col, nil
}

// ---- R3 exam bank (SS3-07) ----

type examImportReq struct {
	Step      string       `json:"step"`
	Questions []repo.ExamQ `json:"questions"`
}

func (h *Handler) ExamImport(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	evID, ok := h.ownedEvent(w, r, orgID)
	if !ok {
		return
	}
	var req examImportReq
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Step == "" || len(req.Questions) == 0 {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "step/questions 必填")
		return
	}
	n, err := h.Repo.ReplaceExamQuestions(r.Context(), evID, req.Step, req.Questions)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "导入失败")
		return
	}
	h.orgAudit(r, orgID, "exam_import", evID, map[string]any{"step": req.Step, "count": n})
	httpx.JSON(w, http.StatusOK, map[string]any{"imported": n})
}

func (h *Handler) ExamGet(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	evID, ok := h.ownedEvent(w, r, orgID)
	if !ok {
		return
	}
	qs, err := h.Repo.ListExamQuestions(r.Context(), evID, r.URL.Query().Get("step"))
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "查询失败")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"questions": qs})
}

// ---- R4 lottery config (SS3-10/12) ----

type poolsReq struct {
	Step  string `json:"step"`
	Pools []struct {
		Code      string `json:"code"`
		Name      string `json:"name"`
		IsDefault bool   `json:"is_default"`
	} `json:"pools"`
}

func (h *Handler) LotteryPools(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	evID, ok := h.ownedEvent(w, r, orgID)
	if !ok {
		return
	}
	var req poolsReq
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Step == "" || len(req.Pools) == 0 {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "step/pools 必填")
		return
	}
	for _, p := range req.Pools {
		if _, err := h.Repo.UpsertLotteryPool(r.Context(), evID, req.Step, p.Code, p.Name, p.IsDefault); err != nil {
			httpx.Fail(w, http.StatusInternalServerError, "db", "保存奖池失败")
			return
		}
	}
	h.orgAudit(r, orgID, "lottery_pools", evID, map[string]any{"step": req.Step, "count": len(req.Pools)})
	httpx.JSON(w, http.StatusOK, map[string]any{"pools": len(req.Pools)})
}

type prizesReq struct {
	Step   string `json:"step"`
	Prizes []struct {
		PoolCode string `json:"pool_code"`
		Code     string `json:"code"`
		Name     string `json:"name"`
		Level    string `json:"level"`
		Stock    int    `json:"stock"`
		Weight   int    `json:"weight"`
		ImageKey string `json:"image_key"`
	} `json:"prizes"`
}

func (h *Handler) LotteryPrizes(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	evID, ok := h.ownedEvent(w, r, orgID)
	if !ok {
		return
	}
	var req prizesReq
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Step == "" || len(req.Prizes) == 0 {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "step/prizes 必填")
		return
	}
	for _, p := range req.Prizes {
		lvl := p.Level
		if lvl == "" {
			lvl = "normal"
		}
		if _, err := h.Repo.UpsertLotteryPrize(r.Context(), evID, req.Step, p.PoolCode,
			p.Code, p.Name, lvl, p.Stock, p.Weight, p.ImageKey); err != nil {
			httpx.Fail(w, http.StatusBadRequest, "bad_pool", "奖项保存失败（pool_code 是否存在）")
			return
		}
	}
	h.orgAudit(r, orgID, "lottery_prizes", evID, map[string]any{"step": req.Step, "count": len(req.Prizes)})
	httpx.JSON(w, http.StatusOK, map[string]any{"prizes": len(req.Prizes)})
}

// LotteryMembershipImport: CSV employee_number,pool_code (SS3-12).
func (h *Handler) LotteryMembershipImport(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	evID, ok := h.ownedEvent(w, r, orgID)
	if !ok {
		return
	}
	step := r.URL.Query().Get("step")
	recs, col, err := readCSV(r)
	if err != nil || step == "" {
		httpx.Fail(w, http.StatusBadRequest, "bad_csv", "需 step 与 CSV(employee_number,pool_code)")
		return
	}
	ei, pi := col["employee_number"], col["pool_code"]
	var rows []repo.LotteryMemberRow
	for _, rec := range recs[1:] {
		if ei >= len(rec) || pi >= len(rec) {
			continue
		}
		emp := strings.TrimSpace(rec[ei])
		pc := strings.TrimSpace(rec[pi])
		if emp != "" && pc != "" {
			rows = append(rows, repo.LotteryMemberRow{EmployeeNumber: emp, PoolCode: pc})
		}
	}
	n, err := h.Repo.ImportLotteryMembership(r.Context(), evID, step, orgID, rows)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "导入失败")
		return
	}
	h.orgAudit(r, orgID, "lottery_membership_import", evID, map[string]any{"step": step, "n": n})
	httpx.JSON(w, http.StatusOK, map[string]any{"parsed": len(rows), "applied": n})
}

// LotteryRigImport: CSV employee_number,prize_code (SS3-12).
func (h *Handler) LotteryRigImport(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	evID, ok := h.ownedEvent(w, r, orgID)
	if !ok {
		return
	}
	step := r.URL.Query().Get("step")
	recs, col, err := readCSV(r)
	if err != nil || step == "" {
		httpx.Fail(w, http.StatusBadRequest, "bad_csv", "需 step 与 CSV(employee_number,prize_code)")
		return
	}
	ei, pi := col["employee_number"], col["prize_code"]
	var rows []repo.LotteryRigRow
	for _, rec := range recs[1:] {
		if ei >= len(rec) || pi >= len(rec) {
			continue
		}
		emp := strings.TrimSpace(rec[ei])
		pc := strings.TrimSpace(rec[pi])
		if emp != "" && pc != "" {
			rows = append(rows, repo.LotteryRigRow{EmployeeNumber: emp, PrizeCode: pc})
		}
	}
	acc, rej, err := h.Repo.ImportLotteryRig(r.Context(), evID, step, orgID, rows)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "导入失败")
		return
	}
	h.orgAudit(r, orgID, "lottery_rig_import", evID,
		map[string]any{"step": step, "accepted": acc, "rejected": rej})
	httpx.JSON(w, http.StatusOK, map[string]any{"accepted": acc, "rejected": rej})
}

func (h *Handler) LotterySummary(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	evID, ok := h.ownedEvent(w, r, orgID)
	if !ok {
		return
	}
	sum, err := h.Repo.LotterySummary(r.Context(), evID, r.URL.Query().Get("step"))
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "查询失败")
		return
	}
	h.orgAudit(r, orgID, "lottery_summary_view", evID, nil)
	httpx.JSON(w, http.StatusOK, sum)
}

// EventConfig: POST /org/events/{id}/config — timezone + identity flags
// + strict fingerprint (SS3-13).
type eventConfigReq struct {
	Timezone          string `json:"timezone"`
	RequireName       *bool  `json:"require_name"`
	RequirePhone      *bool  `json:"require_phone"`
	MultiCompany      *bool  `json:"multi_company"`
	StrictFingerprint *bool  `json:"strict_fingerprint"`
}

func (h *Handler) EventConfig(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.auth(w, r)
	if !ok {
		return
	}
	evID, ok := h.ownedEvent(w, r, orgID)
	if !ok {
		return
	}
	var req eventConfigReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	if req.Timezone != "" {
		if err := h.Repo.SetEventTimezone(r.Context(), evID, orgID, req.Timezone); err != nil {
			httpx.Fail(w, http.StatusInternalServerError, "db", "时区设置失败")
			return
		}
	}
	cur, err := h.Repo.EventIdentity(r.Context(), evID)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "读取配置失败")
		return
	}
	pick := func(p *bool, d bool) bool {
		if p != nil {
			return *p
		}
		return d
	}
	if err := h.Repo.SetEventIdentityFlags(r.Context(), evID, orgID,
		pick(req.RequireName, cur.RequireName),
		pick(req.RequirePhone, cur.RequirePhone),
		pick(req.MultiCompany, cur.MultiCompany)); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "db", "身份配置失败")
		return
	}
	if req.StrictFingerprint != nil {
		_, _ = h.Repo.Pool().Exec(r.Context(),
			`UPDATE event SET strict_fingerprint=$3 WHERE id=$1 AND organizer_id=$2`,
			evID, orgID, *req.StrictFingerprint)
	}
	h.orgAudit(r, orgID, "event_config", evID, nil)
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
