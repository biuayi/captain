package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

const RequestIDHeader = "X-Request-Id"

type ridKey struct{}

// newRID returns a 16-byte hex request id.
func newRID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// RequestID echoes an inbound X-Request-Id or generates one, stores it on the
// response header (so JSON/Fail can include it in the body) and in the request
// context (so logs can reference it). Outermost middleware (DESIGN §3.6).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get(RequestIDHeader)
		if rid == "" {
			rid = newRID()
		}
		w.Header().Set(RequestIDHeader, rid)
		ctx := context.WithValue(r.Context(), ridKey{}, rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDOf returns the request id stored in ctx, or "".
func RequestIDOf(ctx context.Context) string {
	if v, ok := ctx.Value(ridKey{}).(string); ok {
		return v
	}
	return ""
}
