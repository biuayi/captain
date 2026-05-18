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

func TestMigrations_0008_IdentityFactors(t *testing.T) {
	pool := testdb.Pool(t)
	ctx := context.Background()
	has := func(q string, a ...any) bool {
		var n int
		if err := pool.QueryRow(ctx, q, a...).Scan(&n); err != nil {
			t.Fatalf("q %q: %v", q, err)
		}
		return n > 0
	}
	for _, c := range []string{"timezone", "strict_fingerprint", "identity_require_name",
		"identity_require_phone", "identity_multi_company", "flow_template_code"} {
		if !has(`SELECT count(*) FROM information_schema.columns WHERE table_name='event' AND column_name=$1`, c) {
			t.Errorf("event.%s missing", c)
		}
	}
	for _, c := range []string{"company", "claimed_jwt_jti"} {
		if !has(`SELECT count(*) FROM information_schema.columns WHERE table_name='event_whitelist_entry' AND column_name=$1`, c) {
			t.Errorf("event_whitelist_entry.%s missing", c)
		}
	}
	// phone_number/name relaxed to nullable; phone_last4 still NOT NULL
	nn := func(col string) string {
		var s string
		_ = pool.QueryRow(ctx, `SELECT is_nullable FROM information_schema.columns
		    WHERE table_name='event_whitelist_entry' AND column_name=$1`, col).Scan(&s)
		return s
	}
	if nn("phone_number") != "YES" || nn("name") != "YES" {
		t.Error("phone_number/name should be nullable")
	}
	if nn("phone_last4") != "NO" {
		t.Error("phone_last4 must stay NOT NULL")
	}
	if !has(`SELECT count(*) FROM pg_indexes WHERE indexname='uniq_ewe_event_company_employee'`) {
		t.Error("new unique index missing")
	}
	if has(`SELECT count(*) FROM pg_indexes WHERE indexname='uniq_ewe_event_employee'`) {
		t.Error("old unique index should be dropped")
	}
	for _, tbl := range []string{"checkin_day", "participation_warning"} {
		if !has(`SELECT count(*) FROM information_schema.tables WHERE table_name=$1`, tbl) {
			t.Errorf("table %s missing", tbl)
		}
	}
}
