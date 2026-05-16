// Package loginguard hardens backend login (REQ-CHANGE-003 §1):
// a constant 3s response delay regardless of outcome, plus a Redis-backed
// "10 consecutive failures → lock the account 10 minutes" policy. organizer
// and admin are independent scopes.
package loginguard

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	Delay      = 3 * time.Second
	maxFails   = 10
	lockFor    = 10 * time.Minute
	failWindow = 10 * time.Minute
)

type Guard struct{ rdb *redis.Client }

func New(rdb *redis.Client) *Guard { return &Guard{rdb: rdb} }

func failKey(scope, login string) string { return "lg:fail:" + scope + ":" + login }
func lockKey(scope, login string) string { return "lg:lock:" + scope + ":" + login }

// Locked reports whether this account is currently locked out. Fails open
// (not locked) if Redis is unavailable so a Redis outage can't deny all logins.
func (g *Guard) Locked(ctx context.Context, scope, login string) bool {
	n, err := g.rdb.Exists(ctx, lockKey(scope, login)).Result()
	if err != nil {
		return false
	}
	return n == 1
}

// RecordFailure increments the failure counter; on the 10th it sets the lock.
func (g *Guard) RecordFailure(ctx context.Context, scope, login string) {
	k := failKey(scope, login)
	n, err := g.rdb.Incr(ctx, k).Result()
	if err != nil {
		return
	}
	if n == 1 {
		g.rdb.Expire(ctx, k, failWindow)
	}
	if n >= maxFails {
		g.rdb.Set(ctx, lockKey(scope, login), "1", lockFor)
		g.rdb.Del(ctx, k)
	}
}

// Reset clears the failure counter after a successful login.
func (g *Guard) Reset(ctx context.Context, scope, login string) {
	g.rdb.Del(ctx, failKey(scope, login))
}

// Wait sleeps so every login takes a constant ~3s (timing-oracle / brute
// force mitigation), respecting request cancellation.
func Wait(ctx context.Context) {
	t := time.NewTimer(Delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
