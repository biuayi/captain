// Package store wires the external stateful dependencies:
// PostgreSQL (source of truth), Redis (hot path), NATS JetStream (durable async).
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
)

type Store struct {
	PG    *pgxpool.Pool
	Redis *redis.Client
	NC    *nats.Conn
	JS    jetstream.JetStream
}

// Open connects to PG/Redis/NATS with bounded retries so docker-compose
// startup ordering does not require an explicit healthcheck wait.
func Open(ctx context.Context, pgDSN, redisAddr, natsURL string) (*Store, error) {
	pool, err := dialPG(ctx, pgDSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := retry(ctx, "redis", func() error {
		return rdb.Ping(ctx).Err()
	}); err != nil {
		return nil, err
	}

	nc, err := dialNATS(ctx, natsURL)
	if err != nil {
		return nil, fmt.Errorf("nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	if err := ensureStream(ctx, js); err != nil {
		return nil, err
	}

	return &Store{PG: pool, Redis: rdb, NC: nc, JS: js}, nil
}

func (s *Store) Close() {
	if s.PG != nil {
		s.PG.Close()
	}
	if s.Redis != nil {
		_ = s.Redis.Close()
	}
	if s.NC != nil {
		s.NC.Close()
	}
}

func dialPG(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	err := retry(ctx, "postgres", func() error {
		p, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		if err := p.Ping(ctx); err != nil {
			p.Close()
			return err
		}
		pool = p
		return nil
	})
	return pool, err
}

func dialNATS(ctx context.Context, url string) (*nats.Conn, error) {
	var nc *nats.Conn
	err := retry(ctx, "nats", func() error {
		c, err := nats.Connect(url,
			nats.MaxReconnects(-1),
			nats.ReconnectWait(2*time.Second))
		if err != nil {
			return err
		}
		nc = c
		return nil
	})
	return nc, err
}

// CAPTAIN stream backs the durable async subjects (ARCHITECTURE §6).
func ensureStream(ctx context.Context, js jetstream.JetStream) error {
	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "CAPTAIN",
		Subjects: []string{"checkin.>", "participant.>", "export.>"},
		Storage:  jetstream.FileStorage,
		MaxAge:   7 * 24 * time.Hour,
	})
	return err
}

func retry(ctx context.Context, what string, fn func() error) error {
	var last error
	for i := 0; i < 30; i++ {
		if err := fn(); err != nil {
			last = err
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("%s unreachable after retries: %w", what, last)
}
