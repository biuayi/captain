// Command screenstress stresses the big-screen realtime path: many
// concurrent SSE subscribers (simulating screens/viewers) while a flood of
// checkins drives the live counter, measuring fan-out and stability.
//
//	go run ./cmd/screenstress -base http://localhost:8080 -event <id> -token <et> -sse 2000 -checkins 20000 -c 800
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	base := flag.String("base", "http://localhost:8080", "captain base url")
	event := flag.String("event", "", "event id")
	tokenStr := flag.String("token", "", "event_token (for checkin load)")
	sseN := flag.Int("sse", 2000, "concurrent SSE subscribers")
	checkins := flag.Int("checkins", 20000, "total checkins to drive")
	conc := flag.Int("c", 800, "checkin concurrency")
	dur := flag.Int("dur", 45, "max seconds")
	flag.Parse()
	if *event == "" {
		fmt.Println("need -event")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*dur)*time.Second)
	defer cancel()

	var sseUp, sseMsgs, sseErr, lastCount int64
	tr := &http.Transport{MaxIdleConns: *sseN + *conc, MaxIdleConnsPerHost: *sseN + *conc,
		MaxConnsPerHost: *sseN + *conc + 100}
	client := &http.Client{Transport: tr}

	// ---- SSE subscribers ----
	var sseWg sync.WaitGroup
	for i := 0; i < *sseN; i++ {
		sseWg.Add(1)
		go func(idx int) {
			defer sseWg.Done()
			req, _ := http.NewRequestWithContext(ctx, "GET",
				fmt.Sprintf("%s/api/v1/p/e/%s/stream", *base, *event), nil)
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.%d.%d.%d", (idx>>16)&255, (idx>>8)&255, idx&255))
			resp, err := client.Do(req)
			if err != nil {
				atomic.AddInt64(&sseErr, 1)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				atomic.AddInt64(&sseErr, 1)
				return
			}
			atomic.AddInt64(&sseUp, 1)
			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte, 0, 8192), 1<<20)
			for sc.Scan() {
				line := sc.Text()
				if strings.HasPrefix(line, "data:") {
					atomic.AddInt64(&sseMsgs, 1)
					if i := strings.Index(line, `"count":`); i >= 0 {
						s := line[i+8:]
						if j := strings.IndexAny(s, ",}"); j > 0 {
							if v, e := strconv.ParseInt(strings.TrimSpace(s[:j]), 10, 64); e == nil {
								atomic.StoreInt64(&lastCount, v)
							}
						}
					}
				}
				if ctx.Err() != nil {
					return
				}
			}
		}(i)
	}

	time.Sleep(2 * time.Second) // let SSE clients connect

	// ---- checkin flood ----
	var done, okC int64
	start := time.Now()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fmt.Printf("[%4.0fs] sseUp=%d sseErr=%d sseMsgs=%d (%.0f/s) | checkin done=%d ok=%d | screenCount=%d\n",
					time.Since(start).Seconds(),
					atomic.LoadInt64(&sseUp), atomic.LoadInt64(&sseErr),
					atomic.LoadInt64(&sseMsgs),
					float64(atomic.LoadInt64(&sseMsgs))/time.Since(start).Seconds(),
					atomic.LoadInt64(&done), atomic.LoadInt64(&okC),
					atomic.LoadInt64(&lastCount))
			}
		}
	}()

	if *tokenStr != "" {
		sem := make(chan struct{}, *conc)
		var wg sync.WaitGroup
		for i := 0; i < *checkins && ctx.Err() == nil; i++ {
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int) {
				defer wg.Done()
				defer func() { <-sem }()
				defer atomic.AddInt64(&done, 1)
				dev := fmt.Sprintf("ss-%d-%d", start.UnixNano(), idx)
				xff := fmt.Sprintf("11.%d.%d.%d", (idx>>16)&255, (idx>>8)&255, idx&255)
				u := fmt.Sprintf("%s/api/v1/p/e/%s?et=%s&d=%s", *base, *event, *tokenStr, dev)
				rq, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
				rq.Header.Set("X-Forwarded-For", xff)
				rp, err := client.Do(rq)
				if err != nil {
					return
				}
				ck := rp.Header.Get("Set-Cookie")
				io.Copy(io.Discard, rp.Body)
				rp.Body.Close()
				if ck == "" {
					return
				}
				sq, _ := http.NewRequestWithContext(ctx, "POST",
					fmt.Sprintf("%s/api/v1/p/e/%s/steps/s1/submit", *base, *event), nil)
				sq.Header.Set("X-Forwarded-For", xff)
				sq.Header.Set("Cookie", ck)
				sq.Header.Set("Content-Type", "application/json")
				s2, err := client.Do(sq)
				if err != nil {
					return
				}
				if s2.StatusCode == 200 {
					atomic.AddInt64(&okC, 1)
				}
				io.Copy(io.Discard, s2.Body)
				s2.Body.Close()
			}(i)
		}
		wg.Wait()
	}

	time.Sleep(3 * time.Second) // let final SSE snapshots arrive
	cancel()
	sseWg.Wait()

	el := time.Since(start)
	fmt.Println("------------------------------------------------------------")
	fmt.Printf("SSE: target=%d connected=%d errors=%d msgs=%d (%.0f msg/s)\n",
		*sseN, sseUp, sseErr, sseMsgs, float64(sseMsgs)/el.Seconds())
	fmt.Printf("Checkin: done=%d ok=%d  throughput=%.0f/s\n", done, okC, float64(okC)/el.Seconds())
	fmt.Printf("Screen last count=%d  wall=%s\n", lastCount, el.Round(time.Millisecond))
}
