package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/hertz/captain/internal/audit"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/testdb"
)

func TestAuditAppendAndList(t *testing.T) {
	r := repo.New(testdb.Pool(t))
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := r.AppendAudit(ctx, audit.Entry{
			ActorRole: "admin", Action: "config_set", Target: "cf_token",
			Meta: map[string]any{"i": i}, RequestID: "req-1",
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	_ = r.AppendAudit(ctx, audit.Entry{ActorRole: "system", Action: "other"})

	rows, err := r.ListAudit(ctx, "config_set", time.Time{}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) < 3 {
		t.Fatalf("want >=3 config_set rows, got %d", len(rows))
	}
	for _, a := range rows {
		if a.Action != "config_set" {
			t.Fatalf("filter leaked action %q", a.Action)
		}
	}
	// newest-first
	if len(rows) >= 2 && rows[0].CreatedAt.Before(rows[1].CreatedAt) {
		t.Fatal("not ordered newest-first")
	}
}

func TestPlatformConfigRoundtrip(t *testing.T) {
	r := repo.New(testdb.Pool(t))
	ctx := context.Background()

	if err := r.UpsertPlatformConfig(ctx, "k1", []byte("enc-bytes"), "****1234", ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	enc, masked, err := r.GetPlatformConfig(ctx, "k1")
	if err != nil || string(enc) != "enc-bytes" || masked != "****1234" {
		t.Fatalf("get = (%q,%q,%v)", enc, masked, err)
	}
	// upsert overwrites
	if err := r.UpsertPlatformConfig(ctx, "k1", []byte("enc2"), "****9999", ""); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	enc, _, _ = r.GetPlatformConfig(ctx, "k1")
	if string(enc) != "enc2" {
		t.Fatalf("overwrite failed: %q", enc)
	}
	keys, err := r.ListPlatformConfigKeys(ctx)
	if err != nil || keys["k1"] != "****9999" {
		t.Fatalf("list keys = %v (%v)", keys, err)
	}
	if _, _, err := r.GetPlatformConfig(ctx, "missing"); err != repo.ErrNotFound {
		t.Fatalf("missing key err = %v, want ErrNotFound", err)
	}
}
