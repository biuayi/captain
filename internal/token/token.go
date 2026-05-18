// Package token issues and verifies compact HMAC-signed tokens.
//
// Three token kinds share one implementation (see ARCHITECTURE §3):
//   - event_token   : static QR payload, identifies an event, long-lived
//   - device_session: per-device participant session (HttpOnly cookie)
//   - auth          : organizer / admin backend login token
//
// Format: base64url(json(payload)) "." base64url(hmacSHA256). No external deps.
package token

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrMalformed = errors.New("token: malformed")
	ErrSignature = errors.New("token: bad signature")
	ErrExpired   = errors.New("token: expired")
	ErrKind      = errors.New("token: unexpected kind")
)

const (
	KindEvent   = "event_entry"
	KindSession = "participant_session"
	KindAuth    = "auth"
)

const (
	RoleParticipant = "participant"
	RoleOrganizer   = "organizer"
	RoleAdmin       = "admin"
)

type Claims struct {
	Kind       string `json:"k"`
	Subject    string `json:"sub,omitempty"` // organizer/admin id, or device hash
	EventID    string `json:"eid,omitempty"`
	Role       string `json:"role,omitempty"` // organizer | admin | participant
	DeviceHash string `json:"dh,omitempty"`
	JTI        string `json:"jti,omitempty"`
	IssuedAt   int64  `json:"iat"`
	NotBefore  int64  `json:"nbf,omitempty"`
	ExpiresAt  int64  `json:"exp"`
}

type Signer struct{ secret []byte }

func New(secret string) *Signer { return &Signer{secret: []byte(secret)} }

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func unb64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func (s *Signer) sign(payload []byte) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write(payload)
	return b64(m.Sum(nil))
}

// Sign serializes and signs the claims into a compact token.
func (s *Signer) Sign(c Claims) (string, error) {
	if c.IssuedAt == 0 {
		c.IssuedAt = time.Now().Unix()
	}
	if c.JTI == "" {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		c.JTI = base64.RawURLEncoding.EncodeToString(buf)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	p := b64(raw)
	return p + "." + s.sign([]byte(p)), nil
}

// Verify checks signature, expiry/nbf and kind, returning the claims.
func (s *Signer) Verify(tok, expectKind string) (Claims, error) {
	var c Claims
	parts := strings.Split(tok, ".")
	if len(parts) != 2 {
		return c, ErrMalformed
	}
	want := s.sign([]byte(parts[0]))
	if subtle.ConstantTimeCompare([]byte(want), []byte(parts[1])) != 1 {
		return c, ErrSignature
	}
	raw, err := unb64(parts[0])
	if err != nil {
		return c, ErrMalformed
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, ErrMalformed
	}
	now := time.Now().Unix()
	if c.NotBefore != 0 && now < c.NotBefore {
		return c, ErrExpired
	}
	if c.ExpiresAt != 0 && now >= c.ExpiresAt {
		return c, ErrExpired
	}
	if expectKind != "" && c.Kind != expectKind {
		return c, ErrKind
	}
	return c, nil
}
