// Command lotterystress hammers repo.DrawLottery concurrently against a
// configured event/step to verify no oversell and one-draw idempotency
// (SS5-13). Usage:
//
//	CAPTAIN_PG_DSN=... go run ./cmd/lotterystress -event <id> -step <s> -n 500
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hertz/captain/internal/repo"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dsn := flag.String("dsn", "postgres://captain:captain@localhost:5432/captain?sslmode=disable", "pg dsn")
	event := flag.String("event", "", "event id")
	step := flag.String("step", "L", "lottery step id")
	n := flag.Int("n", 500, "concurrent draws (distinct synthetic participants)")
	flag.Parse()
	if *event == "" {
		log.Fatal("-event required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	r := repo.New(pool)

	var win, miss, errs int64
	start := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 64)
	for i := 0; i < *n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			p, _, e := r.UpsertParticipantFull(ctx, repo.ParticipantUpsert{
				EventID: *event, ParticipantKey: fmt.Sprintf("stress-%d-%d", start.UnixNano(), i),
				IdentityType: "external_fingerprint", ParticipantType: "external",
				FingerprintHash: fmt.Sprintf("fp%d", i),
			})
			if e != nil {
				atomic.AddInt64(&errs, 1)
				return
			}
			res, e := r.DrawLottery(ctx, *event, *step, p, "")
			if e != nil {
				atomic.AddInt64(&errs, 1)
				return
			}
			if res.ResolvedBy == "miss" {
				atomic.AddInt64(&miss, 1)
			} else {
				atomic.AddInt64(&win, 1)
			}
		}(i)
	}
	wg.Wait()
	log.Printf("lotterystress: n=%d win=%d miss=%d err=%d in %s",
		*n, win, miss, errs, time.Since(start).Round(time.Millisecond))
}
