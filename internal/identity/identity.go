// Package identity implements REQ-CHANGE-001: server-recomputed browser
// fingerprint + pre-imported whitelist. The server never trusts a
// client-submitted fixed hash — it re-normalizes raw signals and HMACs them
// with a server pepper (codex algorithm定稿).
package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
)

// Error codes surfaced to the participation API (REQ-CHANGE-001 §2).
const (
	ErrBadFingerprint        = "BAD_FINGERPRINT_PAYLOAD"
	ErrStaffNotInWhitelist   = "STAFF_NOT_IN_WHITELIST"
	ErrPhoneMismatch         = "PHONE_MISMATCH"
	ErrEntryBlocked          = "ENTRY_BLOCKED"
	ErrEntryClaimedElsewhere = "ENTRY_CLAIMED_ELSEWHERE"
)

// Signals are the passive browser fingerprint inputs (no IMEI/MAC/contacts).
type Signals struct {
	UAFamilyMajor       string  `json:"uaFamilyMajor"`
	Platform            string  `json:"platform"`
	OS                  string  `json:"os"`
	Language            string  `json:"language"`
	Timezone            string  `json:"timezone"`
	ScreenWidth         int     `json:"screenWidth"`
	ScreenHeight        int     `json:"screenHeight"`
	ColorDepth          int     `json:"colorDepth"`
	DPR                 float64 `json:"dpr"`
	HardwareConcurrency int     `json:"hardwareConcurrency"`
	DeviceMemoryGiB     float64 `json:"deviceMemoryGiB"`
	MaxTouchPoints      int     `json:"maxTouchPoints"`
	WebGLVendorHash     string  `json:"webglVendorHash"`
	WebGLRendererHash   string  `json:"webglRendererHash"`
	CanvasHash          string  `json:"canvasHash"`
	AudioContextHash    string  `json:"audioContextHash"`
}

var errEmpty = errors.New(ErrBadFingerprint)

// Normalize produces the canonical byte payload (fixed, versioned field
// order; 0x1F separators). Any empty field → ErrBadFingerprint so we never
// hash a degenerate fingerprint into a shared key.
func Normalize(s Signals) ([]byte, error) {
	lt := func(v string) string { return strings.ToLower(strings.TrimSpace(v)) }
	fields := [][2]string{
		{"v", "1"},
		{"ua_family_major", lt(s.UAFamilyMajor)},
		{"platform", lt(s.Platform)},
		{"os", lt(s.OS)},
		{"language", lt(s.Language)},
		{"timezone", strings.TrimSpace(s.Timezone)},
		{"screen_width", strconv.Itoa(s.ScreenWidth)},
		{"screen_height", strconv.Itoa(s.ScreenHeight)},
		{"color_depth", strconv.Itoa(s.ColorDepth)},
		{"dpr_milli", strconv.Itoa(int(math.Round(s.DPR * 1000)))},
		{"hardware_concurrency", strconv.Itoa(s.HardwareConcurrency)},
		{"device_memory_mib", strconv.Itoa(int(math.Round(s.DeviceMemoryGiB * 1024)))},
		{"max_touch_points", strconv.Itoa(s.MaxTouchPoints)},
		{"webgl_vendor_hash", lt(s.WebGLVendorHash)},
		{"webgl_renderer_hash", lt(s.WebGLRendererHash)},
		{"canvas_hash", lt(s.CanvasHash)},
		{"audio_context_hash", lt(s.AudioContextHash)},
	}
	var b strings.Builder
	for _, f := range fields {
		if f[1] == "" {
			return nil, errEmpty
		}
		b.WriteString(f[0])
		b.WriteByte('=')
		b.WriteString(f[1])
		b.WriteByte(0x1F)
	}
	return []byte(b.String()), nil
}

// Hash = lowercase hex HMAC-SHA256(pepper, payload).
func Hash(pepper string, payload []byte) string {
	m := hmac.New(sha256.New, []byte(pepper))
	m.Write(payload)
	return hex.EncodeToString(m.Sum(nil))
}

// ParticipantKey derives the stable dedup key (REQ-CHANGE-001 §1.1).
func ParticipantKey(pepper, kind, eventID, tail string) string {
	return Hash(pepper, []byte(kind+":"+eventID+":"+tail))
}

// CompanyNorm normalizes a company name for the whitelist unique key /
// login match: lower-cased, trimmed; empty for single-company events
// (must mirror the SQL expression in migration 0008, SS2-02).
func CompanyNorm(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Last4 returns the trailing 4 digits of a phone string (digits only).
func Last4(phone string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if len(d) < 4 {
		return d
	}
	return d[len(d)-4:]
}
