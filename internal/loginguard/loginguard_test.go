package loginguard

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// downRedis points at a closed port with tight timeouts so every op errors
// fast (no real Redis needed — pure offline unit test).
func downRedis() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:1", MaxRetries: -1,
		DialTimeout: 30 * time.Millisecond, ReadTimeout: 30 * time.Millisecond,
		WriteTimeout: 30 * time.Millisecond, ContextTimeoutEnabled: true,
	})
}

func TestS3_FailClosedDeniesWhenRedisDown(t *testing.T) {
	g := NewWithPolicy(downRedis(), true)
	if !g.Locked(context.Background(), "admin", "u") {
		t.Fatal("fail-closed must deny (locked) when Redis down")
	}
}

func TestS3_InProcessFallbackThrottlesWhenRedisDown(t *testing.T) {
	g := NewWithPolicy(downRedis(), false) // fail-open policy, but in-proc still throttles
	ctx := context.Background()
	const scope, login = "participant:e1", "E1001"

	if g.Locked(ctx, scope, login) {
		t.Fatal("should not be locked initially")
	}
	for i := 0; i < maxFails; i++ {
		g.RecordFailure(ctx, scope, login)
	}
	if !g.Locked(ctx, scope, login) {
		t.Fatal("in-process fallback must lock after maxFails even with Redis down")
	}
	// other login unaffected
	if g.Locked(ctx, scope, "OTHER") {
		t.Fatal("lock must be per-login")
	}
	g.Reset(ctx, scope, login)
	if g.Locked(ctx, scope, login) {
		t.Fatal("Reset must clear the in-process lock")
	}
}
