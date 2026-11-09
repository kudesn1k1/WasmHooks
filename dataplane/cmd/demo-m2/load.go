package main

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const payload = `{"cart_total":1000,"customer":{"id":"c1","lifetime_spend":1500}}`

// window counts what happened in one second (or, summed up, in one act).
type window struct {
	aN, aFail int
	aLat      []time.Duration

	bOK, bQuota, bBusy, bTimeout, bFail int
	bLate                               time.Duration // max lateness of the deadline watcher
	bLateN                              int
}

func (w *window) add(o window) {
	w.aN += o.aN
	w.aFail += o.aFail
	w.aLat = append(w.aLat, o.aLat...)
	w.bOK += o.bOK
	w.bQuota += o.bQuota
	w.bBusy += o.bBusy
	w.bTimeout += o.bTimeout
	w.bFail += o.bFail
	w.bLate = max(w.bLate, o.bLate)
	w.bLateN += o.bLateN
}

func (w window) p99() time.Duration {
	if len(w.aLat) == 0 {
		return 0
	}
	s := slices.Clone(w.aLat)
	slices.Sort(s)
	return s[int(float64(len(s)-1)*0.99)]
}

// stats collects results from the clients and the data plane's log.
type stats struct {
	mu  sync.Mutex
	cur window
}

func (s *stats) shopA(outcome string, lat time.Duration) {
	s.mu.Lock()
	s.cur.aN++
	s.cur.aLat = append(s.cur.aLat, lat)
	if outcome != "ok" {
		s.cur.aFail++
	}
	s.mu.Unlock()
}

func (s *stats) shopB(outcome string) {
	s.mu.Lock()
	switch outcome {
	case "ok":
		s.cur.bOK++
	case "quota_exceeded":
		s.cur.bQuota++
	case "unavailable":
		s.cur.bBusy++
	case "timeout":
		s.cur.bTimeout++
	default:
		s.cur.bFail++
	}
	s.mu.Unlock()
}

// invocation takes shop B's timeouts by the hook's own limit from the data
// plane's log and keeps how late the watcher stopped the script.
func (s *stats) invocation(l invocationLog) {
	if l.TenantID != shopB || l.Outcome != "timeout" || l.Reason != "" {
		return
	}
	s.mu.Lock()
	s.cur.bLate = max(s.cur.bLate, l.Exec-hookTimeout)
	s.cur.bLateN++
	s.mu.Unlock()
}

// flush returns the current window and starts a new one.
func (s *stats) flush() window {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.cur
	s.cur = window{}
	return w
}

// caller sends invoke requests for one shop over its own connections, so
// one shop's traffic never queues behind the other's in the client.
type caller struct {
	client *http.Client
	url    string
}

func newCaller(base string, conns int) *caller {
	return &caller{
		client: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			MaxIdleConns: conns, MaxIdleConnsPerHost: conns, MaxConnsPerHost: conns,
		}},
		url: base + "/v1/hooks/" + hookName + "/invoke",
	}
}

func (c *caller) call(ctx context.Context, tenant string) (string, time.Duration) {
	body := `{"tenant_id":"` + tenant + `","payload":` + payload + `}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(body))
	if err != nil {
		return "error", 0
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return "error", time.Since(start)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.Header.Get("X-Hook-Outcome"), time.Since(start)
}

// steady sends rps calls per second for tenant until ctx ends, on schedule:
// a slow answer does not delay the next call, as with real buyers.
func steady(ctx context.Context, wg *sync.WaitGroup, c *caller, tenant string, rps int, record func(string, time.Duration)) {
	wg.Go(func() {
		start := time.Now()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		sent := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			for due := int(time.Since(start).Seconds() * float64(rps)); sent < due; sent++ {
				wg.Go(func() {
					outcome, lat := c.call(context.WithoutCancel(ctx), tenant)
					if ctx.Err() == nil {
						record(outcome, lat)
					}
				})
			}
		}
	})
}

// buyers keeps n calls of tenant going: each buyer calls again as soon as
// it has an answer, and waits 20ms after a refusal, like a client that backs
// off a little.
func buyers(ctx context.Context, wg *sync.WaitGroup, c *caller, tenant string, n int, record func(string)) {
	for range n {
		wg.Go(func() {
			for ctx.Err() == nil {
				outcome, _ := c.call(ctx, tenant)
				if ctx.Err() != nil {
					return
				}
				record(outcome)
				if outcome == "unavailable" || outcome == "quota_exceeded" || outcome == "error" {
					time.Sleep(20 * time.Millisecond)
				}
			}
		})
	}
}
