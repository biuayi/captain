// Package realtime owns the live attendance count: Redis hot counter,
// Redis pub/sub fan-out, an in-process SSE hub with <=2/s throttling, and
// a 10s PG reconciliation loop (ARCHITECTURE §5).
package realtime

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/hertz/captain/internal/repo"
	"github.com/redis/go-redis/v9"
)

type Snapshot struct {
	EventID string `json:"event_id"`
	Count   int64  `json:"count"`
	TS      int64  `json:"ts"`
}

func countKey(eventID string) string { return "count:" + eventID + ":checkin" }
func chanKey(eventID string) string  { return "rt:event:" + eventID }

type Manager struct {
	rdb  *redis.Client
	repo *repo.Repo

	mu     sync.Mutex
	latest map[string]Snapshot                 // event -> latest snapshot
	dirty  map[string]bool                     // event -> needs flush
	subs   map[string]map[chan []byte]struct{} // event -> subscribers
}

func New(rdb *redis.Client, r *repo.Repo) *Manager {
	return &Manager{
		rdb: rdb, repo: r,
		latest: map[string]Snapshot{},
		dirty:  map[string]bool{},
		subs:   map[string]map[chan []byte]struct{}{},
	}
}

// OnCheckin bumps the hot counter and broadcasts. Called once per *new* checkin.
func (m *Manager) OnCheckin(ctx context.Context, eventID string) {
	n, err := m.rdb.Incr(ctx, countKey(eventID)).Result()
	if err != nil {
		log.Printf("realtime: incr %s: %v", eventID, err)
		return
	}
	m.publish(ctx, Snapshot{EventID: eventID, Count: n, TS: time.Now().UnixMilli()})
}

func (m *Manager) publish(ctx context.Context, s Snapshot) {
	b, _ := json.Marshal(s)
	if err := m.rdb.Publish(ctx, chanKey(s.EventID), b).Err(); err != nil {
		log.Printf("realtime: publish %s: %v", s.EventID, err)
	}
}

// Snapshot returns the current count, falling back to PG if Redis is cold.
func (m *Manager) Snapshot(ctx context.Context, eventID string) Snapshot {
	n, err := m.rdb.Get(ctx, countKey(eventID)).Int64()
	if err != nil {
		if pg, e := m.repo.CheckinCount(ctx, eventID); e == nil {
			n = pg
			m.rdb.Set(ctx, countKey(eventID), n, 0)
		}
	}
	return Snapshot{EventID: eventID, Count: n, TS: time.Now().UnixMilli()}
}

// Subscribe registers an SSE client; the returned channel receives JSON
// snapshots. The caller must invoke the cancel func on disconnect.
func (m *Manager) Subscribe(ctx context.Context, eventID string) (<-chan []byte, func()) {
	ch := make(chan []byte, 4)
	m.mu.Lock()
	if m.subs[eventID] == nil {
		m.subs[eventID] = map[chan []byte]struct{}{}
	}
	m.subs[eventID][ch] = struct{}{}
	m.mu.Unlock()

	// Immediate snapshot on connect (reconnect compensation, ARCHITECTURE §5).
	if b, err := json.Marshal(m.Snapshot(ctx, eventID)); err == nil {
		ch <- b
	}

	cancel := func() {
		m.mu.Lock()
		delete(m.subs[eventID], ch)
		m.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

// Run blocks: pub/sub ingest + 500ms throttled fan-out + 10s reconcile.
func (m *Manager) Run(ctx context.Context) {
	ps := m.rdb.PSubscribe(ctx, "rt:event:*")
	defer ps.Close()
	msgs := ps.Channel()

	flush := time.NewTicker(500 * time.Millisecond) // <=2 pushes/sec
	reconcile := time.NewTicker(10 * time.Second)
	defer flush.Stop()
	defer reconcile.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-msgs:
			var s Snapshot
			if json.Unmarshal([]byte(msg.Payload), &s) == nil {
				m.mu.Lock()
				m.latest[s.EventID] = s
				m.dirty[s.EventID] = true
				m.mu.Unlock()
			}
		case <-flush.C:
			m.flush()
		case <-reconcile.C:
			m.reconcile(ctx)
		}
	}
}

// ---- fan-out / reconcile ----

func (m *Manager) flush() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for eventID, isDirty := range m.dirty {
		if !isDirty {
			continue
		}
		s := m.latest[eventID]
		b, _ := json.Marshal(s)
		for ch := range m.subs[eventID] {
			select {
			case ch <- b:
			default: // slow client: drop, next snapshot is authoritative
			}
		}
		m.dirty[eventID] = false
	}
}

func (m *Manager) reconcile(ctx context.Context) {
	ids, err := m.repo.ActiveEventIDs(ctx)
	if err != nil {
		log.Printf("realtime: reconcile list: %v", err)
		return
	}
	for _, id := range ids {
		pgN, err := m.repo.CheckinCount(ctx, id)
		if err != nil {
			continue
		}
		redisN, _ := m.rdb.Get(ctx, countKey(id)).Int64()
		if redisN != pgN {
			m.rdb.Set(ctx, countKey(id), pgN, 0)
			m.publish(ctx, Snapshot{EventID: id, Count: pgN, TS: time.Now().UnixMilli()})
			log.Printf("realtime: reconciled %s redis=%d -> pg=%d", id, redisN, pgN)
		}
	}
}
