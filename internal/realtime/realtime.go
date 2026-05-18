// Package realtime owns the live big-screen feed: Redis hot counter, Redis
// pub/sub fan-out, an in-process SSE hub with <=2/s count throttling, a 10s
// PG reconcile, plus low-frequency winner / milestone envelopes consumed
// from NATS prize.won (DESIGN §SS-6).
package realtime

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/hertz/captain/internal/repo"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
)

// Snapshot is the count envelope. Type is "count"; the top-level Count field
// is kept for backward compatibility with existing big-screen clients.
type Snapshot struct {
	Type    string `json:"type"` // count
	EventID string `json:"event_id"`
	Count   int64  `json:"count"`
	TS      int64  `json:"ts"`
}

// Winner is the marquee envelope for a grand-prize win (SS6-03).
type Winner struct {
	Type    string `json:"type"` // winner
	EventID string `json:"event_id"`
	Name    string `json:"name"`
	Prize   string `json:"prize"`
	TS      int64  `json:"ts"`
}

func countKey(eventID string) string  { return "count:" + eventID + ":checkin" }
func chanKey(eventID string) string   { return "rt:event:" + eventID }
func winnerKey(eventID string) string { return "win:" + eventID }

const winnerCap = 50

type Manager struct {
	rdb  *redis.Client
	repo *repo.Repo

	mu     sync.Mutex
	latest map[string]Snapshot
	dirty  map[string]bool
	subs   map[string]map[chan []byte]struct{}
}

func New(rdb *redis.Client, r *repo.Repo) *Manager {
	return &Manager{
		rdb: rdb, repo: r,
		latest: map[string]Snapshot{},
		dirty:  map[string]bool{},
		subs:   map[string]map[chan []byte]struct{}{},
	}
}

// OnParticipated bumps the participated counter and broadcasts (D5; called
// once when a participant first completes an enabled stage, SS6-02).
func (m *Manager) OnParticipated(ctx context.Context, eventID string) {
	n, err := m.rdb.Incr(ctx, countKey(eventID)).Result()
	if err != nil {
		log.Printf("realtime: incr %s: %v", eventID, err)
		return
	}
	s := Snapshot{Type: "count", EventID: eventID, Count: n, TS: time.Now().UnixMilli()}
	m.apply(s)
	m.publish(ctx, s)
}

// OnPrizeWon records a grand-prize winner (capped Redis list) and fans the
// winner envelope out immediately, cross-instance (SS6-03).
func (m *Manager) OnPrizeWon(ctx context.Context, eventID, name, prize string) {
	wn := Winner{Type: "winner", EventID: eventID, Name: name, Prize: prize, TS: time.Now().UnixMilli()}
	b, _ := json.Marshal(wn)
	pipe := m.rdb.Pipeline()
	pipe.LPush(ctx, winnerKey(eventID), b)
	pipe.LTrim(ctx, winnerKey(eventID), 0, winnerCap-1)
	_, _ = pipe.Exec(ctx)
	m.broadcast(eventID, b)
	_ = m.rdb.Publish(ctx, chanKey(eventID), b).Err() // cross-instance
}

// Milestone fans out a low-frequency arbitrary envelope (e.g. completed
// count), not throttled (SS6-05).
func (m *Manager) Milestone(ctx context.Context, eventID string, payload map[string]any) {
	payload["type"] = "milestone"
	payload["event_id"] = eventID
	b, _ := json.Marshal(payload)
	m.broadcast(eventID, b)
	_ = m.rdb.Publish(ctx, chanKey(eventID), b).Err()
}

func (m *Manager) apply(s Snapshot) {
	m.mu.Lock()
	m.latest[s.EventID] = s
	m.dirty[s.EventID] = true
	m.mu.Unlock()
}

// broadcast pushes a payload to all local subscribers immediately.
func (m *Manager) broadcast(eventID string, b []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subs[eventID] {
		select {
		case ch <- b:
		default:
		}
	}
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
		if pg, e := m.repo.ParticipatedCount(ctx, eventID); e == nil {
			n = pg
			m.rdb.Set(ctx, countKey(eventID), n, 0)
		}
	}
	return Snapshot{Type: "count", EventID: eventID, Count: n, TS: time.Now().UnixMilli()}
}

// Subscribe registers an SSE client. On connect it gets the current count
// snapshot plus the recent winners (transient marquee backfill, SS6-04).
func (m *Manager) Subscribe(ctx context.Context, eventID string) (<-chan []byte, func()) {
	ch := make(chan []byte, 16)
	m.mu.Lock()
	if m.subs[eventID] == nil {
		m.subs[eventID] = map[chan []byte]struct{}{}
	}
	m.subs[eventID][ch] = struct{}{}
	m.mu.Unlock()

	if b, err := json.Marshal(m.Snapshot(ctx, eventID)); err == nil {
		ch <- b
	}
	if recent, err := m.rdb.LRange(ctx, winnerKey(eventID), 0, 9).Result(); err == nil {
		for i := len(recent) - 1; i >= 0; i-- { // oldest first
			select {
			case ch <- []byte(recent[i]):
			default:
			}
		}
	}

	cancel := func() {
		m.mu.Lock()
		delete(m.subs[eventID], ch)
		m.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

// Run blocks: pub/sub ingest (count throttled, winner/milestone immediate)
// + 500ms flush + 10s reconcile.
func (m *Manager) Run(ctx context.Context) {
	ps := m.rdb.PSubscribe(ctx, "rt:event:*")
	defer ps.Close()
	msgs := ps.Channel()

	flush := time.NewTicker(500 * time.Millisecond)
	reconcile := time.NewTicker(10 * time.Second)
	defer flush.Stop()
	defer reconcile.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			var probe struct {
				Type    string `json:"type"`
				EventID string `json:"event_id"`
			}
			if json.Unmarshal([]byte(msg.Payload), &probe) != nil {
				continue
			}
			if probe.Type == "count" || probe.Type == "" {
				var s Snapshot
				if json.Unmarshal([]byte(msg.Payload), &s) == nil {
					m.apply(s)
				}
			} else { // winner / milestone: immediate, low-frequency
				m.broadcast(probe.EventID, []byte(msg.Payload))
			}
		case <-flush.C:
			m.flush()
		case <-reconcile.C:
			m.reconcile(ctx)
		}
	}
}

// ConsumePrizes subscribes the durable NATS prize.won subject and turns each
// grand-prize win into a winner envelope (SS6-03). Run in its own goroutine.
func (m *Manager) ConsumePrizes(ctx context.Context, js jetstream.JetStream) {
	if js == nil {
		return
	}
	cons, err := js.CreateOrUpdateConsumer(ctx, "CAPTAIN", jetstream.ConsumerConfig{
		Durable:       "realtime-prizes",
		FilterSubject: "prize.won",
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    3,
	})
	if err != nil {
		log.Printf("realtime: prize consumer: %v", err)
		return
	}
	_, err = cons.Consume(func(msg jetstream.Msg) {
		var p struct {
			EventID string `json:"event_id"`
			Prize   string `json:"prize"`
			Name    string `json:"name"`
		}
		if json.Unmarshal(msg.Data(), &p) == nil && p.EventID != "" {
			name := p.Name
			if name == "" {
				name = "中奖者"
			}
			m.OnPrizeWon(ctx, p.EventID, name, p.Prize)
		}
		_ = msg.Ack()
	})
	if err != nil {
		log.Printf("realtime: prize consume: %v", err)
	}
}

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
			default:
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
		pgN, err := m.repo.ParticipatedCount(ctx, id)
		if err != nil {
			continue
		}
		redisN, _ := m.rdb.Get(ctx, countKey(id)).Int64()
		if redisN != pgN {
			m.rdb.Set(ctx, countKey(id), pgN, 0)
			s := Snapshot{Type: "count", EventID: id, Count: pgN, TS: time.Now().UnixMilli()}
			m.apply(s)
			m.publish(ctx, s)
			log.Printf("realtime: reconciled %s redis=%d -> pg=%d", id, redisN, pgN)
		}
	}
}
