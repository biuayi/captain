package httpx_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/orgperm"
	"github.com/hertz/captain/internal/testdb"
	"github.com/hertz/captain/internal/token"
)

func orgTok(t *testing.T, sig *token.Signer, ver int, perms map[string]bool) string {
	t.Helper()
	tok, err := sig.Sign(token.Claims{
		Kind: token.KindAuth, Role: token.RoleOrganizer, Subject: "org-1",
		Perm: perms, PermVersion: ver, ExpiresAt: 1 << 62,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestOrgPerm(t *testing.T) {
	rdb := testdb.Redis(t)
	sig := token.New("s3cret")
	cache := orgperm.New(rdb)
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	run := func(mw func(http.Handler) http.Handler, tok string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		httpx.RequestID(mw(okHandler)).ServeHTTP(rec, req)
		return rec.Code
	}
	loadV := func(v int, err error) httpx.VersionLoader {
		return func(context.Context, string) (int, error) { return v, err }
	}

	t.Run("no token -> 401", func(t *testing.T) {
		if c := run(httpx.OrgPerm(sig, cache, loadV(1, nil), ""), ""); c != 401 {
			t.Fatalf("code=%d want 401", c)
		}
	})
	t.Run("missing perm -> 403", func(t *testing.T) {
		cache.Invalidate(context.Background(), "org-1")
		tok := orgTok(t, sig, 1, map[string]bool{})
		if c := run(httpx.OrgPerm(sig, cache, loadV(1, nil), "can_create_event"), tok); c != 403 {
			t.Fatalf("code=%d want 403", c)
		}
	})
	t.Run("fresh version -> 200", func(t *testing.T) {
		cache.Invalidate(context.Background(), "org-1")
		tok := orgTok(t, sig, 5, map[string]bool{"can_create_event": true})
		if c := run(httpx.OrgPerm(sig, cache, loadV(5, nil), "can_create_event"), tok); c != 200 {
			t.Fatalf("code=%d want 200", c)
		}
	})
	t.Run("stale version -> 401 token_stale", func(t *testing.T) {
		cache.Invalidate(context.Background(), "org-1")
		tok := orgTok(t, sig, 4, map[string]bool{"x": true})
		if c := run(httpx.OrgPerm(sig, cache, loadV(9, nil), "x"), tok); c != 401 {
			t.Fatalf("code=%d want 401", c)
		}
	})
	t.Run("loader error + cache miss -> fail-open 200", func(t *testing.T) {
		cache.Invalidate(context.Background(), "org-1")
		tok := orgTok(t, sig, 2, map[string]bool{"x": true})
		if c := run(httpx.OrgPerm(sig, cache, loadV(0, errors.New("pg down")), "x"), tok); c != 200 {
			t.Fatalf("code=%d want 200 (fail-open)", c)
		}
	})
}
