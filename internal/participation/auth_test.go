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
	"github.com/hertz/captain/internal/loginguard"
	"github.com/hertz/captain/internal/participation"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/testdb"
	"github.com/hertz/captain/internal/token"
	"github.com/hertz/captain/internal/turnstile"
)

func TestParticipantLoginFlow(t *testing.T) {
	pool := testdb.Pool(t)
	rdb := testdb.Redis(t)
	r := repo.New(pool)
	sig := token.New("p-secret")
	ctx := context.Background()

	// fixtures: organizer → flow → active event → whitelist entry
	suffix := time.Now().UnixNano()
	orgID, err := r.CreateOrganizer(ctx, "Co", fmt.Sprintf("co-%d", suffix), "h")
	if err != nil {
		t.Fatal(err)
	}
	flowID, err := r.CreateFlowConfig(ctx, orgID, "f",
		[]byte(`{"version":1,"flowId":"f","name":"n","entryStepId":"s1","steps":[{"id":"s1","type":"checkin"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	evID, err := r.CreateEvent(ctx, orgID, "E", time.Now().Add(-time.Hour),
		time.Now().Add(24*time.Hour), 100, "ink-wash-default", flowID)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetEventStatus(ctx, evID, orgID, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.InsertWhitelist(ctx, evID, orgID, "b1", []repo.WLImportRow{
		{EmployeeNumber: "E1001", Name: "张三", Phone: "+8613800001234", PhoneLast4: "1234"},
	}); err != nil {
		t.Fatal(err)
	}

	h := &participation.Handler{
		Repo: r, Sig: sig, RL: httpx.NewRateLimiter(rdb),
		Guard: loginguard.New(rdb), RDB: rdb, TS: turnstile.New("off", "", ""),
		Pepper: "pep",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /p/e/{event_id}/login", h.Login)
	mux.HandleFunc("POST /p/e/{event_id}/logout", h.Logout)
	mux.HandleFunc("GET /p/e/{event_id}/me", h.Me)
	srv := httptest.NewServer(httpx.RequestID(mux))
	defer srv.Close()

	login := func(body string) (int, map[string]any) {
		rs, err := http.Post(srv.URL+"/p/e/"+evID+"/login", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(rs.Body).Decode(&m)
		return rs.StatusCode, m
	}
	me := func(tok string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/p/e/"+evID+"/me", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rs, _ := http.DefaultClient.Do(req)
		rs.Body.Close()
		return rs.StatusCode
	}

	// wrong phone_last4 → 401
	if c, _ := login(`{"employee_number":"E1001","phone_last4":"9999"}`); c != http.StatusUnauthorized {
		t.Fatalf("bad last4 = %d want 401", c)
	}
	// correct → 200 + participant JWT
	c, m := login(`{"employee_number":"E1001","phone_last4":"1234"}`)
	if c != http.StatusOK {
		t.Fatalf("login = %d (%v)", c, m)
	}
	tok1 := m["token"].(string)
	cl, err := sig.Verify(tok1, token.KindAuth)
	if err != nil || cl.Role != token.RoleParticipant || cl.EventID != evID || cl.JTI == "" {
		t.Fatalf("token claims wrong: %+v err=%v", cl, err)
	}
	if me(tok1) != http.StatusOK {
		t.Fatal("me with fresh token should be 200")
	}

	// re-login (顶号): old token superseded
	_, m2 := login(`{"employee_number":"E1001","phone_last4":"1234"}`)
	tok2 := m2["token"].(string)
	if tok2 == tok1 {
		t.Fatal("re-login should rotate jti")
	}
	if me(tok1) != http.StatusUnauthorized {
		t.Fatal("old token must be superseded after re-login")
	}
	if me(tok2) != http.StatusOK {
		t.Fatal("new token should work")
	}

	// logout revokes tok2
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/p/e/"+evID+"/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tok2)
	rs, _ := http.DefaultClient.Do(req)
	rs.Body.Close()
	if me(tok2) != http.StatusUnauthorized {
		t.Fatal("token must be revoked after logout")
	}

	// unbind → entry reusable, re-login succeeds
	// find entry id
	var entryID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM event_whitelist_entry WHERE event_id=$1 AND employee_number='E1001'`, evID).
		Scan(&entryID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UnbindWhitelist(ctx, entryID, orgID); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if c, _ := login(`{"employee_number":"E1001","phone_last4":"1234"}`); c != http.StatusOK {
		t.Fatalf("re-login after unbind = %d want 200", c)
	}
}
