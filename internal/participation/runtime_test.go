package participation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/participation"
	"github.com/hertz/captain/internal/realtime"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/storage"
	"github.com/hertz/captain/internal/testdb"
	"github.com/hertz/captain/internal/token"
	"github.com/hertz/captain/internal/turnstile"
)

func TestRuntimeStageGatingAndScoring(t *testing.T) {
	pool := testdb.Pool(t)
	rdb := testdb.Redis(t)
	r := repo.New(pool)
	sig := token.New("rt")
	ctx := context.Background()
	suf := time.Now().UnixNano()

	orgID, _ := r.CreateOrganizer(ctx, "C", fmt.Sprintf("c-%d", suf), "h")
	flowJSON := `{"version":2,"flowId":"f","name":"n","entryStepId":"r1","steps":[
	 {"id":"r1","type":"checkin","stage":"R1","nextStepId":"r2","config":{"days":1}},
	 {"id":"r2","type":"form","stage":"R2","nextStepId":"r3","config":{"fields":[]}},
	 {"id":"r3","type":"exam","stage":"R3","nextStepId":null,"config":{"mode":"all","passScore":5}}]}`
	flowID, _ := r.CreateFlowConfig(ctx, orgID, "f", []byte(flowJSON))
	evID, _ := r.CreateEvent(ctx, orgID, "E", time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour), 10, "ink-wash-default", flowID)
	_ = r.SetEventStatus(ctx, evID, orgID, "active")
	if _, err := r.ReplaceExamQuestions(ctx, evID, "r3", []repo.ExamQ{
		{Stem: "2+2", Options: []string{"3", "4"}, Correct: []int{1}, Score: 5},
	}); err != nil {
		t.Fatal(err)
	}

	pid, _, err := r.UpsertParticipantFull(ctx, repo.ParticipantUpsert{
		EventID: evID, ParticipantKey: "k-" + fmt.Sprint(suf),
		IdentityType: "external_fingerprint", ParticipantType: "external", FingerprintHash: "fp",
	})
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := sig.Sign(token.Claims{Kind: token.KindAuth, Role: token.RoleParticipant,
		Subject: pid, EventID: evID, JTI: "j1", ExpiresAt: 1 << 62})

	st, _ := storage.New(storage.Options{Driver: "local", Dir: t.TempDir()})
	h := &participation.Handler{
		Repo: r, Sig: sig, RT: realtime.New(rdb, r), RL: httpx.NewRateLimiter(rdb),
		TS: turnstile.New("off", "", ""), RDB: rdb, Store: st, Pepper: "p",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /p/e/{event_id}/steps/{step_id}/submit", h.Submit)
	srv := httptest.NewServer(httpx.RequestID(mux))
	defer srv.Close()

	post := func(step, body string) (int, map[string]any) {
		rq, _ := http.NewRequest(http.MethodPost, srv.URL+"/p/e/"+evID+"/steps/"+step+"/submit", strings.NewReader(body))
		rq.Header.Set("Authorization", "Bearer "+tok)
		rs, e := http.DefaultClient.Do(rq)
		if e != nil {
			t.Fatal(e)
		}
		defer rs.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(rs.Body).Decode(&m)
		return rs.StatusCode, m
	}

	// R2 before R1 → gated
	if c, _ := post("r2", `{"fields":{"a":"b"}}`); c != http.StatusConflict {
		t.Fatalf("R2 before R1 = %d want 409", c)
	}
	// R1 checkin (days=1) completes R1
	c, m := post("r1", `{}`)
	if c != http.StatusOK || m["stage_complete"] != true {
		t.Fatalf("R1 = %d %v", c, m)
	}
	// R2 now allowed
	if c, _ := post("r2", `{"fields":{"name":"x","photo_image":"uploads/k"}}`); c != http.StatusOK {
		t.Fatalf("R2 after R1 = %d", c)
	}
	// R3 exam scored
	c, m = post("r3", `{"answers":{"0":[1]}}`)
	if c != http.StatusOK || m["passed"] != true || m["score"].(float64) != 5 {
		t.Fatalf("R3 = %d %v", c, m)
	}

	// participated counted, completed-all set, data fields written
	if n, _ := r.ParticipatedCount(ctx, evID); n != 1 {
		t.Fatalf("participated = %d want 1", n)
	}
	fn, _ := r.EventFunnel(ctx, evID)
	if fn["completed"].(int64) != 1 {
		t.Fatalf("completed funnel = %v want 1", fn["completed"])
	}
	var f1, f2 *string
	_ = pool.QueryRow(ctx, `SELECT data_field_1, data_field_2 FROM participation WHERE id=(SELECT id FROM participation WHERE event_id=$1 AND participant_id=$2)`, evID, pid).Scan(&f1, &f2)
	if f2 == nil || *f2 != "uploads/k" {
		t.Fatalf("data_field_2 (oss key) = %v want uploads/k", f2)
	}
}
