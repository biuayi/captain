package participation

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hertz/captain/internal/flow"
	"github.com/hertz/captain/internal/httpx"
)

// summarizeForm derives the two generic record columns from R2 form fields:
// data_field_1 = compact text of non-resource fields; data_field_2 = the
// first OSS resource key field (key name containing "image"/"file"/"oss")
// (DESIGN §SS-4/§SS-7).
func summarizeForm(fields map[string]any) (f1, f2 string) {
	if fields == nil {
		return "", ""
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := fmt.Sprintf("%v", fields[k])
		lk := strings.ToLower(k)
		if f2 == "" && (strings.Contains(lk, "image") || strings.Contains(lk, "file") || strings.Contains(lk, "oss") || strings.Contains(lk, "upload")) {
			f2 = v
			continue
		}
		parts = append(parts, k+"="+v)
	}
	f1 = strings.Join(parts, "; ")
	if len(f1) > 2000 {
		f1 = f1[:2000]
	}
	return f1, f2
}

// pickQuestions deterministically selects exam questions for a participant so
// a refresh is stable (SS4-08). mode "all" → all; "random" → randomCount
// chosen by a participant+step seeded shuffle.
func pickQuestions(all []examQ, step flow.Step, seed string) []examQ {
	mode, _ := step.Config["mode"].(string)
	if mode != "random" {
		return all
	}
	n := 0
	if rc, ok := step.Config["randomCount"].(float64); ok {
		n = int(rc)
	}
	if n <= 0 || n >= len(all) {
		return all
	}
	idx := make([]int, len(all))
	for i := range idx {
		idx[i] = i
	}
	h := sha256.Sum256([]byte(seed))
	r := binary.LittleEndian.Uint64(h[:8])
	// deterministic Fisher–Yates with an LCG seeded by the hash
	for i := len(idx) - 1; i > 0; i-- {
		r = r*6364136223846793005 + 1442695040888963407
		j := int(r >> 33 % uint64(i+1))
		idx[i], idx[j] = idx[j], idx[i]
	}
	out := make([]examQ, 0, n)
	for _, i := range idx[:n] {
		out = append(out, all[i])
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Idx < out[b].Idx })
	return out
}

type examQ struct {
	Idx     int
	Stem    string
	Options []string
	Correct []int
	Score   int
	Multi   bool
}

func (h *Handler) loadExam(ctx context.Context, eventID, stepID string) []examQ {
	rows, err := h.Repo.ListExamQuestions(ctx, eventID, stepID)
	if err != nil {
		return nil
	}
	out := make([]examQ, 0, len(rows))
	for _, q := range rows {
		out = append(out, examQ{q.Idx, q.Stem, q.Options, q.Correct, q.Score, q.Multi})
	}
	return out
}

// scoreExam grades server-side; answers are picked option indexes per
// question idx. Multi-choice requires the exact set (SS4-09).
func (h *Handler) scoreExam(ctx context.Context, eventID, stepID string, step flow.Step, answers map[string][]int) (score, total int, passed bool) {
	qs := pickQuestions(h.loadExam(ctx, eventID, stepID), step, "exam:"+eventID+":"+stepID)
	for _, q := range qs {
		total += q.Score
		picked := answers[fmt.Sprintf("%d", q.Idx)]
		if sameSet(picked, q.Correct) {
			score += q.Score
		}
	}
	pass := 0
	if ps, ok := step.Config["passScore"].(float64); ok {
		pass = int(ps)
	}
	return score, total, score >= pass
}

func sameSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	am := map[int]bool{}
	for _, x := range a {
		am[x] = true
	}
	for _, x := range b {
		if !am[x] {
			return false
		}
	}
	return true
}

// StepGet: GET /api/v1/p/e/{event_id}/steps/{step_id} — runtime step state.
// For exam: returns the participant's deterministic question set WITHOUT
// correct answers (anti-cheat, SS4-08). For checkin: days progress.
func (h *Handler) StepGet(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	stepID := r.PathValue("step_id")
	claims, ok := h.participantAuth(w, r, eventID)
	if !ok {
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
	_, doneSet, current, _ := h.Repo.StageState(r.Context(), eventID, claims.Subject)
	out := map[string]any{
		"step_id": stepID, "type": step.Type, "stage": step.Stage,
		"can_enter": fl.CanEnter(step.Stage, doneSet), "current_stage": current,
	}
	switch step.Type {
	case flow.StepExam:
		qs := pickQuestions(h.loadExam(r.Context(), eventID, stepID), step, "exam:"+eventID+":"+stepID)
		safe := make([]map[string]any, 0, len(qs))
		for _, q := range qs {
			safe = append(safe, map[string]any{
				"idx": q.Idx, "stem": q.Stem, "options": q.Options,
				"score": q.Score, "multi": q.Multi}) // no Correct
		}
		out["questions"] = safe
	case flow.StepCheckin:
		if partID, _, _, e := h.Repo.StageState(r.Context(), eventID, claims.Subject); e == nil {
			n, _ := h.Repo.DistinctCheckinDays(r.Context(), partID)
			out["days_done"] = n
		}
		out["config"] = step.Config
	default:
		out["config"] = step.Config
	}
	httpx.JSON(w, http.StatusOK, out)
}

// Upload: POST /api/v1/p/e/{event_id}/uploads — R2 image/file to storage
// (participant JWT, MIME/size whitelist), returns the object key (SS4-06).
var uploadMIME = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true,
	"application/pdf": true,
}

func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	claims, ok := h.participantAuth(w, r, eventID)
	if !ok {
		return
	}
	if !h.RL.Allow(r.Context(), "upload:p:"+claims.Subject, 20, time.Minute) {
		httpx.Fail(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "需 multipart 上传")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, "bad_request", "缺少 file 字段")
		return
	}
	defer file.Close()
	mime := hdr.Header.Get("Content-Type")
	if !uploadMIME[mime] {
		httpx.Fail(w, http.StatusUnsupportedMediaType, "bad_mime", "不支持的文件类型: "+mime)
		return
	}
	if hdr.Size > 10<<20 {
		httpx.Fail(w, http.StatusRequestEntityTooLarge, "too_large", "文件过大(>10MB)")
		return
	}
	key := fmt.Sprintf("uploads/%s/%s/%d_%s", eventID, claims.Subject, time.Now().UnixNano(), hdr.Filename)
	if _, err := h.Store.Put(key, file); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "storage", "存储失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"key": key})
}
