package platformcfg_test

import (
	"context"
	"testing"

	"github.com/hertz/captain/internal/platformcfg"
)

type fakeStore struct {
	enc    map[string][]byte
	masked map[string]string
}

func newFake() *fakeStore {
	return &fakeStore{enc: map[string][]byte{}, masked: map[string]string{}}
}
func (f *fakeStore) GetPlatformConfig(_ context.Context, k string) ([]byte, string, error) {
	b, ok := f.enc[k]
	if !ok {
		return nil, "", context.Canceled // any error → treated as "not in db"
	}
	return b, f.masked[k], nil
}
func (f *fakeStore) UpsertPlatformConfig(_ context.Context, k string, v []byte, m, _ string) error {
	f.enc[k] = v
	f.masked[k] = m
	return nil
}

func TestManager_DBEnvNone(t *testing.T) {
	fs := newFake()
	envv := map[string]string{"cf_token": "env-val"}
	m := platformcfg.New(fs, "a-strong-config-key", func(k string) string { return envv[k] })
	ctx := context.Background()

	if !m.Enabled() {
		t.Fatal("should be enabled with config key")
	}
	// none → env fallback
	if v, src := m.Get(ctx, "cf_token"); v != "env-val" || src != "env" {
		t.Fatalf("env fallback = (%q,%q)", v, src)
	}
	if _, src := m.Get(ctx, "absent"); src != "none" {
		t.Fatalf("absent src = %q want none", src)
	}
	// set → db wins, decrypts back
	if err := m.Set(ctx, "cf_token", "secret-123", "admin-1"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if v, src := m.Get(ctx, "cf_token"); v != "secret-123" || src != "db" {
		t.Fatalf("db get = (%q,%q)", v, src)
	}
	if m := platformcfg.Mask("secret-123"); m != "****-123" {
		t.Fatalf("mask = %q", m)
	}
}

func TestManager_DisabledWithoutKey(t *testing.T) {
	fs := newFake()
	m := platformcfg.New(fs, "", func(string) string { return "" })
	if m.Enabled() {
		t.Fatal("must be disabled without config key")
	}
	if err := m.Set(context.Background(), "k", "v", ""); err == nil {
		t.Fatal("Set must error when disabled")
	}
}
