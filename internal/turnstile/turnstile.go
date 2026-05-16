// Package turnstile verifies Cloudflare Turnstile tokens server-side
// (REQ-CHANGE-003 §2). Config-driven: mode "off" skips verification entirely
// so the existing offline demo / load path is unaffected; "enforce" requires
// a valid token (needs CF egress + real keys, a deploy prerequisite).
package turnstile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const siteVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

type Verifier struct {
	Mode    string // off | enforce
	SiteKey string
	secret  string
	hc      *http.Client
}

func New(mode, siteKey, secret string) *Verifier {
	return &Verifier{
		Mode: mode, SiteKey: siteKey, secret: secret,
		// Transport uses ProxyFromEnvironment → honors HTTPS_PROXY so the
		// container can reach Cloudflare through the host proxy if set.
		hc: &http.Client{Timeout: 8 * time.Second},
	}
}

func (v *Verifier) Enabled() bool { return v.Mode == "enforce" }

// Verify returns true when verification passes OR when disabled (mode != enforce).
func (v *Verifier) Verify(ctx context.Context, token, remoteIP string) bool {
	if !v.Enabled() {
		return true
	}
	if token == "" || v.secret == "" {
		return false
	}
	form := url.Values{"secret": {v.secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, siteVerifyURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := v.hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var out struct {
		Success bool `json:"success"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return false
	}
	return out.Success
}
