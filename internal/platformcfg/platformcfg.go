// Package platformcfg resolves platform secrets (Cloudflare / Aliyun tokens)
// from the encrypted platform_config table, falling back to environment
// variables, with an in-process cache (DESIGN §3.3/§SS-0). The DB value is
// AES-256-GCM encrypted; the API never exposes plaintext, only a tail mask.
package platformcfg

import (
	"context"
	"crypto/sha256"
	"sync"

	"github.com/hertz/captain/internal/cryptobox"
)

// Store is the persistence dependency (implemented by repo).
type Store interface {
	GetPlatformConfig(ctx context.Context, key string) (valueEnc []byte, masked string, err error)
	UpsertPlatformConfig(ctx context.Context, key string, valueEnc []byte, masked, updatedBy string) error
}

type Manager struct {
	store Store
	key   []byte // 32B derived from CAPTAIN_CONFIG_KEY; nil disables DB secrets
	env   func(string) string
	mu    sync.RWMutex
	cache map[string]string
}

// New derives a 32-byte AES key from configKey (sha256; empty disables DB
// secret storage). envFallback maps a logical key to an env value ("" if unset).
func New(store Store, configKey string, envFallback func(string) string) *Manager {
	var k []byte
	if configKey != "" {
		s := sha256.Sum256([]byte(configKey))
		k = s[:]
	}
	if envFallback == nil {
		envFallback = func(string) string { return "" }
	}
	return &Manager{store: store, key: k, env: envFallback, cache: map[string]string{}}
}

// Enabled reports whether DB-backed encrypted secrets are usable.
func (m *Manager) Enabled() bool { return m.key != nil }

// Get returns the value and its source: "db", "env", or "none".
func (m *Manager) Get(ctx context.Context, key string) (string, string) {
	m.mu.RLock()
	if v, ok := m.cache[key]; ok {
		m.mu.RUnlock()
		return v, "db"
	}
	m.mu.RUnlock()

	if m.key != nil {
		if enc, _, err := m.store.GetPlatformConfig(ctx, key); err == nil {
			if pt, err := cryptobox.Open(m.key, enc); err == nil {
				v := string(pt)
				m.mu.Lock()
				m.cache[key] = v
				m.mu.Unlock()
				return v, "db"
			}
		}
	}
	if v := m.env(key); v != "" {
		return v, "env"
	}
	return "", "none"
}

// Set encrypts and persists a secret, then invalidates the cache.
func (m *Manager) Set(ctx context.Context, key, plaintext, updatedBy string) error {
	if m.key == nil {
		return cryptobox.ErrBadKey
	}
	enc, err := cryptobox.Seal(m.key, []byte(plaintext))
	if err != nil {
		return err
	}
	if err := m.store.UpsertPlatformConfig(ctx, key, enc, Mask(plaintext), updatedBy); err != nil {
		return err
	}
	m.Invalidate(key)
	return nil
}

// Invalidate drops a cached value (after a Set or external change).
func (m *Manager) Invalidate(key string) {
	m.mu.Lock()
	delete(m.cache, key)
	m.mu.Unlock()
}

// Mask renders a non-reversible display hint: last 4 chars revealed.
func Mask(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}
