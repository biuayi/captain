package realtime_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hertz/captain/internal/realtime"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/testdb"
)

func TestRealtimeEnvelopes(t *testing.T) {
	rdb := testdb.Redis(t)
	m := realtime.New(rdb, repo.New(testdb.Pool(t)))
	ctx := context.Background()
	const ev = "ev-rt-1"

	// count envelope keeps backward-compatible top-level count
	m.OnParticipated(ctx, ev)
	m.OnParticipated(ctx, ev)
	snap := m.Snapshot(ctx, ev)
	if snap.Type != "count" || snap.Count != 2 {
		t.Fatalf("snapshot = %+v want type=count count=2", snap)
	}

	// subscribe gets a count snapshot first
	ch, cancel := m.Subscribe(ctx, ev)
	defer cancel()
	select {
	case b := <-ch:
		var s realtime.Snapshot
		if json.Unmarshal(b, &s) != nil || s.Type != "count" || s.Count != 2 {
			t.Fatalf("first SSE msg = %s", b)
		}
	case <-time.After(time.Second):
		t.Fatal("no snapshot on subscribe")
	}

	// winner envelope is pushed to the capped list + broadcast immediately
	m.OnPrizeWon(ctx, ev, "张总", "特等奖")
	select {
	case b := <-ch:
		var wn realtime.Winner
		if json.Unmarshal(b, &wn) != nil || wn.Type != "winner" || wn.Name != "张总" || wn.Prize != "特等奖" {
			t.Fatalf("winner msg = %s", b)
		}
	case <-time.After(time.Second):
		t.Fatal("no winner broadcast")
	}
	n, _ := rdb.LLen(ctx, "win:"+ev).Result()
	if n != 1 {
		t.Fatalf("winner list len = %d want 1", n)
	}
}
