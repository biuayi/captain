package orgperm_test

import (
	"context"
	"testing"

	"github.com/hertz/captain/internal/orgperm"
	"github.com/hertz/captain/internal/testdb"
)

func TestCache_RoundtripAndInvalidate(t *testing.T) {
	rdb := testdb.Redis(t)
	c := orgperm.New(rdb)
	ctx := context.Background()

	if _, hit := c.Get(ctx, "org-1"); hit {
		t.Fatal("expected miss on empty cache")
	}
	c.Set(ctx, "org-1", 7)
	if v, hit := c.Get(ctx, "org-1"); !hit || v != 7 {
		t.Fatalf("Get = (%d,%v), want (7,true)", v, hit)
	}
	c.Invalidate(ctx, "org-1")
	if _, hit := c.Get(ctx, "org-1"); hit {
		t.Fatal("expected miss after Invalidate")
	}
}
