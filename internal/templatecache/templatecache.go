// Package templatecache is a short-TTL Redis cache for organizer-visible
// template lists (DESIGN §SS-1, SS1-09). Fail-open: any Redis error falls
// back to the caller's DB query.
package templatecache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hertz/captain/internal/domain"
	"github.com/redis/go-redis/v9"
)

const ttl = 60 * time.Second

type Cache struct{ rdb *redis.Client }

func New(rdb *redis.Client) *Cache { return &Cache{rdb: rdb} }

func key(kind, orgID string) string { return "tpl:list:" + kind + ":" + orgID }

// Get returns the cached list and true on a hit.
func (c *Cache) Get(ctx context.Context, kind, orgID string) ([]domain.Template, bool) {
	b, err := c.rdb.Get(ctx, key(kind, orgID)).Bytes()
	if err != nil {
		return nil, false
	}
	var ts []domain.Template
	if json.Unmarshal(b, &ts) != nil {
		return nil, false
	}
	return ts, true
}

// Set caches the list (best-effort).
func (c *Cache) Set(ctx context.Context, kind, orgID string, ts []domain.Template) {
	b, err := json.Marshal(ts)
	if err != nil {
		return
	}
	c.rdb.Set(ctx, key(kind, orgID), b, ttl)
}

// Invalidate drops all template list caches (called on any admin template
// write; templates are low-write so a broad flush is fine).
func (c *Cache) Invalidate(ctx context.Context) {
	iter := c.rdb.Scan(ctx, 0, "tpl:list:*", 100).Iterator()
	for iter.Next(ctx) {
		c.rdb.Del(ctx, iter.Val())
	}
}
