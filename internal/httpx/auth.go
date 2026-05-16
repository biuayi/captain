package httpx

import (
	"net/http"
	"strings"
	"time"
)

const SessionCookie = "cap_sess"

// BearerToken extracts the Authorization: Bearer <tok> value.
func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// SetSessionCookie writes the HttpOnly device-session cookie.
// secure must be set when served over HTTPS (codex review).
func SetSessionCookie(w http.ResponseWriter, value string, exp time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  exp,
	})
}

func SessionToken(r *http.Request) string {
	if c, err := r.Cookie(SessionCookie); err == nil {
		return c.Value
	}
	return ""
}
