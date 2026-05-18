package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hertz/captain/internal/admin"
	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/loginguard"
	"github.com/hertz/captain/internal/orgperm"
	"github.com/hertz/captain/internal/platformcfg"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/testdb"
	"github.com/hertz/captain/internal/token"
	"github.com/hertz/captain/internal/turnstile"
)

func TestAdminFlow(t *testing.T) {
	r := repo.New(testdb.Pool(t))
	rdb := testdb.Redis(t)
	sig := token.New("admin-secret")
	h := &admin.Handler{
		Repo: r, Sig: sig, Guard: loginguard.New(rdb),
		TS:    turnstile.New("off", "", ""),
		PC:    platformcfg.New(r, "unit-test-config-key", func(string) string { return "" }),
		OrgPC: orgperm.New(rdb),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /a/organizers", h.ListOrganizers)
	mux.HandleFunc("POST /a/organizers", h.CreateOrganizer)
	mux.HandleFunc("PATCH /a/organizers/{id}/permissions", h.SetOrganizerPermissions)
	mux.HandleFunc("POST /a/organizers/{id}/password", h.ResetOrganizerPassword)
	mux.HandleFunc("DELETE /a/organizers/{id}", h.DeleteOrganizer)
	mux.HandleFunc("PUT /a/config/{key}", h.PutConfig)
	mux.HandleFunc("GET /a/config", h.GetConfig)
	mux.HandleFunc("GET /a/audit", h.ListAudit)
	srv := httptest.NewServer(httpx.RequestID(mux))
	defer srv.Close()

	const adminID = "00000000-0000-0000-0000-000000000001" // actor_id is uuid
	adminTok, _ := sig.Sign(token.Claims{
		Kind: token.KindAuth, Role: token.RoleAdmin, Subject: adminID, ExpiresAt: 1 << 62})

	do := func(method, path, tok, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}

	// auth required
	if code, _ := do(http.MethodGet, "/a/organizers", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("no-token list = %d, want 401", code)
	}

	// create organizer
	login := fmt.Sprintf("acme-%d", time.Now().UnixNano())
	code, m := do(http.MethodPost, "/a/organizers", adminTok,
		fmt.Sprintf(`{"name":"ACME","login_name":%q,"password":"pw123456"}`, login))
	if code != http.StatusCreated {
		t.Fatalf("create = %d (%v)", code, m)
	}
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatal("no organizer id returned")
	}

	// set permissions -> perm_version bumps to 2
	code, m = do(http.MethodPatch, "/a/organizers/"+id+"/permissions", adminTok,
		`{"can_create_event":true,"can_view_records":false,"can_export_records":false}`)
	if code != http.StatusOK || m["perm_version"].(float64) != 2 {
		t.Fatalf("setperms = %d (%v)", code, m)
	}

	// reset password
	if code, _ := do(http.MethodPost, "/a/organizers/"+id+"/password", adminTok,
		`{"password":"newpw123"}`); code != http.StatusOK {
		t.Fatalf("resetpw = %d", code)
	}

	// put config (enabled, key has config key)
	if code, _ := do(http.MethodPut, "/a/config/cloudflare_turnstile_secret", adminTok,
		`{"value":"super-secret-token"}`); code != http.StatusOK {
		t.Fatalf("putconfig = %d", code)
	}
	// get config: never leaks plaintext, shows masked
	code, m = do(http.MethodGet, "/a/config", adminTok, "")
	cfg := m["config"].(map[string]any)["cloudflare_turnstile_secret"].(map[string]any)
	if cfg["set"] != true {
		t.Fatalf("config not marked set: %v", cfg)
	}
	if mk, _ := cfg["masked"].(string); strings.Contains(mk, "super-secret") {
		t.Fatalf("plaintext leaked in mask: %q", mk)
	}

	// delete (soft) then it disappears from list
	if code, _ := do(http.MethodDelete, "/a/organizers/"+id, adminTok, ""); code != http.StatusOK {
		t.Fatalf("delete = %d", code)
	}
	_, m = do(http.MethodGet, "/a/organizers", adminTok, "")
	for _, o := range m["organizers"].([]any) {
		if om, _ := o.(map[string]any); om != nil && om["id"] == id {
			t.Fatal("soft-deleted organizer still listed")
		}
	}

	// audit captured the actions
	code, m = do(http.MethodGet, "/a/audit?action=organizer_permissions", adminTok, "")
	if code != http.StatusOK || m["total"].(float64) < 1 {
		t.Fatalf("audit = %d (%v)", code, m)
	}
}
