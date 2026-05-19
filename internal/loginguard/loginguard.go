// Package loginguard hardens backend/participant login (DESIGN §SS-2):
// a constant 3s response delay regardless of outcome, plus a Redis-backed
// "10 consecutive failures → lock 10 minutes" policy. Scopes are independent
// (organizer / admin / participant:{event}).
//
// S3 strengthening: an in-process fallback counter throttles brute force
// per-instance even when Redis is unavailable; CAPTAIN_LOGIN_FAILCLOSED
// additionally denies login outright on Redis errors.
package loginguard

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	Delay      = 3 * time.Second
	maxFails   = 10
	lockFor    = 10 * time.Minute
	failWindow = 10 * time.Minute
)

type memCounter struct {
	fails     int
	winUntil  time.Time
	lockUntil time.Time
}

type Guard struct {
	rdb        *redis.Client
	failClosed bool

	mu  sync.Mutex
	mem map[string]*memCounter // scope:login → per-instance fallback
}

func New(rdb *redis.Client) *Guard { return NewWithPolicy(rdb, false) }

func NewWithPolicy(rdb *redis.Client, failClosed bool) *Guard {
	return &Guard{rdb: rdb, failClosed: failClosed, mem: map[string]*memCounter{}}
}

func failKey(scope, login string) string { return "lg:fail:" + scope + ":" + login }
func lockKey(scope, login string) string { return "lg:lock:" + scope + ":" + login }

// memLocked reports the in-process fallback lock state (and prunes expired).
func (g *Guard) memLocked(scope, login string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.mem[scope+":"+login]
	if c == nil {
		return false
	}
	now := time.Now()
	if now.After(c.lockUntil) && now.After(c.winUntil) {
		delete(g.mem, scope+":"+login) // fully expired → reclaim
		return false
	}
	return now.Before(c.lockUntil)
}

func (g *Guard) memFail(scope, login string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := scope + ":" + login
	c := g.mem[key]
	now := time.Now()
	if c == nil || now.After(c.winUntil) {
		c = &memCounter{winUntil: now.Add(failWindow)}
		g.mem[key] = c
	}
	c.fails++
	if c.fails >= maxFails {
		c.lockUntil = now.Add(lockFor)
		c.fails = 0
	}
}

func (g *Guard) memReset(scope, login string) {
	g.mu.Lock()
	delete(g.mem, scope+":"+login)
	g.mu.Unlock()
}

// Locked reports whether this account is currently locked out. On Redis
// error: deny when failClosed, else fall back to the in-process counter
// (so a Redis outage still throttles brute force per-instance, S3).
func (g *Guard) Locked(ctx context.Context, scope, login string) bool {
	n, err := g.rdb.Exists(ctx, lockKey(scope, login)).Result()
	if err != nil {
		if g.failClosed {
			return true
		}
		return g.memLocked(scope, login)
	}
	return n == 1 || g.memLocked(scope, login)
}

// RecordFailure increments the Redis counter (lock on the 10th) and always
// also increments the in-process fallback counter (S3).
func (g *Guard) RecordFailure(ctx context.Context, scope, login string) {
	g.memFail(scope, login)
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
	g.memReset(scope, login)
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
