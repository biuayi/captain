package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDOf(r.Context())
		Fail(w, http.StatusBadRequest, "x", "y")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if seen == "" || len(seen) != 32 {
		t.Fatalf("ctx request id = %q, want 32-hex", seen)
	}
	if got := rec.Header().Get(RequestIDHeader); got != seen {
		t.Fatalf("header %q != ctx %q", got, seen)
	}
	if body := rec.Body.String(); !contains(body, seen) {
		t.Fatalf("body %s missing request_id %s", body, seen)
	}
}

func TestRequestID_EchoesInbound(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := RequestIDOf(r.Context()); got != "abc-123" {
			t.Fatalf("ctx = %q, want echoed abc-123", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "abc-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get(RequestIDHeader) != "abc-123" {
		t.Fatalf("header not echoed: %q", rec.Header().Get(RequestIDHeader))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
