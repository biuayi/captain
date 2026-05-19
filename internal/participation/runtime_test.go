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

// TestBootstrapIdentityFlags asserts that GET /p/e/{event_id} returns an
// "identity" object with require_name/require_phone/multi_company matching the
// event's stored identity-factor flags (F2-01; decision: landing not /p/config).
func TestBootstrapIdentityFlags(t *testing.T) {
	pool := testdb.Pool(t)
	rdb := testdb.Redis(t)
	r := repo.New(pool)
	sig := token.New("bs")
	ctx := context.Background()
	suf := time.Now().UnixNano()

	orgID, _ := r.CreateOrganizer(ctx, "C2", fmt.Sprintf("c2-%d", suf), "h")
	flowJSON := `{"version":2,"flowId":"f2","name":"n2","entryStepId":"s1","steps":[{"id":"s1","type":"checkin","stage":"R1"}]}`
	flowID, _ := r.CreateFlowConfig(ctx, orgID, "f2", []byte(flowJSON))
	evID, _ := r.CreateEvent(ctx, orgID, "BootstrapE", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), 10, "ink-wash-default", flowID)
	_ = r.SetEventStatus(ctx, evID, orgID, "active")

	// Set identity flags: require_name=true, require_phone=true, multi_company=false
	if err := r.SetEventIdentityFlags(ctx, evID, orgID, true, true, false); err != nil {
		t.Fatal(err)
	}

	h := &participation.Handler{
		Repo: r, Sig: sig, RT: realtime.New(rdb, r), RL: httpx.NewRateLimiter(rdb),
		TS: turnstile.New("off", "", ""), RDB: rdb,
		Store: func() storage.Storage { s, _ := storage.New(storage.Options{Driver: "local", Dir: t.TempDir()}); return s }(),
		Pepper: "p",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /p/e/{event_id}", h.Bootstrap)
	srv := httptest.NewServer(httpx.RequestID(mux))
	defer srv.Close()

	// mint a valid event token
	et, _ := sig.Sign(token.Claims{Kind: token.KindEvent, EventID: evID, ExpiresAt: 1 << 62})

	resp, err := http.Get(srv.URL + "/p/e/" + evID + "?et=" + et)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Bootstrap status = %d want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	iRaw, ok := body["identity"]
	if !ok {
		t.Fatal("response missing 'identity' field")
	}
	iMap, ok := iRaw.(map[string]any)
	if !ok {
		t.Fatalf("'identity' is not an object: %T", iRaw)
	}
	if iMap["require_name"] != true {
		t.Errorf("identity.require_name = %v want true", iMap["require_name"])
	}
	if iMap["require_phone"] != true {
		t.Errorf("identity.require_phone = %v want true", iMap["require_phone"])
	}
	if iMap["multi_company"] != false {
		t.Errorf("identity.multi_company = %v want false", iMap["multi_company"])
	}
}

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
	// R1 checkin (days=1) completes R1; carry device_id (G2) + geo (G7)
	c, m := post("r1", `{"device_id":"dev-XYZ-9","geo":{"lat":31.23,"lng":121.47,"accuracy":8}}`)
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
	var f1, f2, dev *string
	_ = pool.QueryRow(ctx, `SELECT data_field_1, data_field_2, device_id FROM participation WHERE event_id=$1 AND participant_id=$2`, evID, pid).Scan(&f1, &f2, &dev)
	if f2 == nil || *f2 != "uploads/k" {
		t.Fatalf("data_field_2 (oss key) = %v want uploads/k", f2)
	}
	if dev == nil || *dev != "dev-XYZ-9" {
		t.Fatalf("device_id = %v want dev-XYZ-9 (G2)", dev)
	}
	// G7: per-day checkin geo must surface in records (was empty for v2)
	prs, err := r.ListParticipants(ctx, evID)
	if err != nil || len(prs) != 1 {
		t.Fatalf("ListParticipants = %d %v", len(prs), err)
	}
	if prs[0].Lat == nil || *prs[0].Lat < 31.0 || *prs[0].Lat > 31.5 {
		t.Fatalf("record lat = %v want ~31.23 (G7 checkin_day geo)", prs[0].Lat)
	}
}
