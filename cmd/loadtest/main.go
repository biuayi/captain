// Command loadtest simulates N users scanning the QR and checking in,
// concurrently. Each virtual user gets a unique device id AND a unique
// X-Forwarded-For so the per-IP/per-device rate limiters reflect distinct
// clients (single-box load would otherwise be throttled).
//
//	go run ./cmd/loadtest -base http://localhost:8080 -event <id> -token <et> -n 30000 -c 2000
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	base := flag.String("base", "http://localhost:8080", "captain base url")
	event := flag.String("event", "", "event id")
	token := flag.String("token", "", "event_token")
	n := flag.Int("n", 30000, "total simulated users")
	c := flag.Int("c", 2000, "max in-flight (flash-crowd concurrency)")
	flag.Parse()
	if *event == "" || *token == "" {
		fmt.Println("need -event and -token")
		os.Exit(2)
	}

	tr := &http.Transport{
		MaxIdleConns:        *c * 2,
		MaxIdleConnsPerHost: *c * 2,
		MaxConnsPerHost:     *c * 2,
		IdleConnTimeout:     30 * time.Second,
	}
	client := &http.Client{Transport: tr, Timeout: 40 * time.Second}

	var done, okCheck, failBoot, failCheck, rl int64
	start := time.Now()

	// progress ticker
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				d := atomic.LoadInt64(&done)
				el := time.Since(start).Seconds()
				fmt.Printf("[%4.0fs] done=%d ok=%d bootFail=%d checkFail=%d rl=%d  %.0f req/s\n",
					el, d, atomic.LoadInt64(&okCheck), atomic.LoadInt64(&failBoot),
					atomic.LoadInt64(&failCheck), atomic.LoadInt64(&rl), float64(d)/el)
			}
		}
	}()

	sem := make(chan struct{}, *c)
	var wg sync.WaitGroup
	for i := 0; i < *n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			defer atomic.AddInt64(&done, 1)

			dev := fmt.Sprintf("load-%d-%d", start.UnixNano(), idx)
			xff := fmt.Sprintf("10.%d.%d.%d", (idx>>16)&255, (idx>>8)&255, idx&255)

			// 1) scan -> bootstrap (mint device-session)
			bootURL := fmt.Sprintf("%s/api/v1/p/e/%s?et=%s&d=%s", *base, *event, *token, dev)
			req, _ := http.NewRequestWithContext(context.Background(), "GET", bootURL, nil)
			req.Header.Set("X-Forwarded-For", xff)
			resp, err := client.Do(req)
			if err != nil {
				atomic.AddInt64(&failBoot, 1)
				return
			}
			cookie := resp.Header.Get("Set-Cookie")
			st := resp.StatusCode
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if st != 200 || cookie == "" {
				if st == 429 {
					atomic.AddInt64(&rl, 1)
				}
				atomic.AddInt64(&failBoot, 1)
				return
			}

			// 2) submit checkin step s1
			subURL := fmt.Sprintf("%s/api/v1/p/e/%s/steps/s1/submit", *base, *event)
			req2, _ := http.NewRequestWithContext(context.Background(), "POST", subURL, nil)
			req2.Header.Set("X-Forwarded-For", xff)
			req2.Header.Set("Cookie", cookie)
			req2.Header.Set("Content-Type", "application/json")
			resp2, err := client.Do(req2)
			if err != nil {
				atomic.AddInt64(&failCheck, 1)
				return
			}
			st2 := resp2.StatusCode
			io.Copy(io.Discard, resp2.Body)
			resp2.Body.Close()
			if st2 == 200 {
				atomic.AddInt64(&okCheck, 1)
			} else {
				if st2 == 429 {
					atomic.AddInt64(&rl, 1)
				}
				atomic.AddInt64(&failCheck, 1)
			}
		}(i)
	}
	wg.Wait()
	close(stop)

	el := time.Since(start)
	fmt.Println("------------------------------------------------------------")
	fmt.Printf("total=%d ok_checkin=%d bootFail=%d checkFail=%d rateLimited=%d\n",
		*n, okCheck, failBoot, failCheck, rl)
	fmt.Printf("wall=%s  throughput=%.0f checkins/s\n", el.Round(time.Millisecond),
		float64(okCheck)/el.Seconds())
}
