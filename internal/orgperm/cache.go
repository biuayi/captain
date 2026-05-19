// Package orgperm caches the organizer permission version so a long-lived
// organizer JWT (carrying a perm snapshot) can be invalidated the moment a
// super-admin changes permissions (DESIGN §3.1/§SS-0). PG is the source of
// truth (organizer.perm_version); Redis perm:org:{id} is a hot cache.
package orgperm

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const ttl = 10 * time.Minute

func key(orgID string) string { return "perm:org:" + orgID }

type Cache struct{ rdb *redis.Client }

func New(rdb *redis.Client) *Cache { return &Cache{rdb: rdb} }

// Get returns the cached perm version and whether it was a cache hit.
func (c *Cache) Get(ctx context.Context, orgID string) (int, bool) {
	v, err := c.rdb.Get(ctx, key(orgID)).Int()
	if err != nil {
		return 0, false
	}
	return v, true
}

// Set caches the authoritative version.
func (c *Cache) Set(ctx context.Context, orgID string, v int) {
	c.rdb.Set(ctx, key(orgID), strconv.Itoa(v), ttl)
}

// Invalidate drops the cache entry (call after a permission change).
func (c *Cache) Invalidate(ctx context.Context, orgID string) {
	c.rdb.Del(ctx, key(orgID))
}
