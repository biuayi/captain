package store_test

import (
	"context"
	"testing"

	"github.com/hertz/captain/internal/store"
	"github.com/hertz/captain/internal/testdb"
)

func TestMigrations_0006_PlatformBase(t *testing.T) {
	pool := testdb.Pool(t) // skips if no DB; runs all migrations

	// Idempotent: a second Migrate is a no-op (tracked in schema_migrations).
	if err := store.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("re-Migrate not idempotent: %v", err)
	}

	ctx := context.Background()
	has := func(q string, args ...any) bool {
		var n int
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		return n > 0
	}

	for _, col := range []string{"can_create_event", "can_view_records", "can_export_records", "deleted_at", "perm_version"} {
		if !has(`SELECT count(*) FROM information_schema.columns WHERE table_name='organizer' AND column_name=$1`, col) {
			t.Errorf("organizer.%s missing", col)
		}
	}
	if !has(`SELECT count(*) FROM information_schema.columns WHERE table_name='export_job' AND column_name='kind'`) {
		t.Error("export_job.kind missing")
	}
	for _, tbl := range []string{"platform_config", "audit_log"} {
		if !has(`SELECT count(*) FROM information_schema.tables WHERE table_name=$1`, tbl) {
			t.Errorf("table %s missing", tbl)
		}
	}
}
