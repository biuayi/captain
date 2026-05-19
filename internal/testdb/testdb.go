// Package testdb provides shared test fixtures for DB/Redis/NATS-backed tests.
// Every helper SKIPS (not fails) when the backing service is unreachable, so
// `go test ./...` stays green in environments without infra; bring infra up
// with scripts/testdb.sh to exercise these paths.
package testdb

import (
	"context"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/hertz/captain/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// Pool returns a migrated pgx pool, or skips the test if Postgres is down.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := env("CAPTAIN_TEST_PG_DSN", "postgres://captain:captain@localhost:5432/captain?sslmode=disable")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("testdb: no postgres (%v)", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("testdb: postgres unreachable (%v)", err)
	}
	if err := store.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("testdb: migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Redis returns a flushed redis client on a per-test isolated logical DB
// (1..15) so parallel test packages don't FlushDB each other, or skips if
// Redis is down.
func Redis(t testing.TB) *redis.Client {
	t.Helper()
	db := rand.Intn(15) + 1
	rdb := redis.NewClient(&redis.Options{Addr: env("CAPTAIN_TEST_REDIS_ADDR", "localhost:6379"), DB: db})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		t.Skipf("testdb: no redis (%v)", err)
	}
	rdb.FlushDB(ctx)
	t.Cleanup(func() {
		fctx, fcancel := context.WithTimeout(context.Background(), 2*time.Second)
		rdb.FlushDB(fctx)
		fcancel()
		_ = rdb.Close()
	})
	return rdb
}

// JetStream returns a JetStream context, or skips if NATS is down.
func JetStream(t testing.TB) (jetstream.JetStream, func()) {
	t.Helper()
	nc, err := nats.Connect(env("CAPTAIN_TEST_NATS_URL", "nats://localhost:4222"), nats.Timeout(3*time.Second))
	if err != nil {
		t.Skipf("testdb: no nats (%v)", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		t.Skipf("testdb: jetstream (%v)", err)
	}
	cleanup := func() { nc.Close() }
	t.Cleanup(cleanup)
	return js, cleanup
}
