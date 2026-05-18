package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/token"
)

func TestAuthClaims(t *testing.T) {
	sig := token.New("k")
	mk := func(role string) string {
		tok, _ := sig.Sign(token.Claims{Kind: token.KindAuth, Role: role, Subject: "s", ExpiresAt: 1 << 62})
		return tok
	}
	req := func(tok string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		return r
	}

	if _, ok := httpx.AuthClaims(req(mk(token.RoleOrganizer)), sig, token.RoleOrganizer); !ok {
		t.Fatal("valid organizer token should pass")
	}
	if _, ok := httpx.AuthClaims(req(mk(token.RoleAdmin)), sig, token.RoleOrganizer); ok {
		t.Fatal("admin token must fail organizer role check")
	}
	if _, ok := httpx.AuthClaims(req(""), sig, ""); ok {
		t.Fatal("absent token must fail")
	}
	if _, ok := httpx.AuthClaims(req("garbage.tok"), sig, ""); ok {
		t.Fatal("malformed token must fail")
	}
}
