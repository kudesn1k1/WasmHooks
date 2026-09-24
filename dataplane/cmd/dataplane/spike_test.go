package main

import (
	"bufio"
	"context"
	"flag"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Spike S12: end-to-end latency of a warm invocation through the real
// binary (HTTP, gateway, input and output schema checks, executor, pool,
// Extism). Run with:
//
//	go test -run Spike -v ./cmd/dataplane/ -args -spike

var spike = flag.Bool("spike", false, "run spike measurements")

func TestSpikeInvokeLatency(t *testing.T) {
	if !*spike {
		t.Skip("spike measurement; run with -spike")
	}
	snapshot, modules := writeEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	go run(ctx, []string{"-listen", "127.0.0.1:0", "-snapshot", snapshot, "-modules-dir", modules, "-log-level", "error"}, pw)
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, pr)
	url := "http://" + strings.TrimSpace(strings.TrimPrefix(line, "listening on ")) + "/v1/hooks/checkout.discount/invoke"
	body := `{"tenant_id":"merchant-a","payload":{"cart_total":100,"customer":{"id":"c1","lifetime_spend":1500}}}`
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64}}

	invoke := func() time.Duration {
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+e2eToken)
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		d := time.Since(start)
		if resp.Header.Get("X-Hook-Outcome") != "ok" {
			t.Fatalf("outcome %q", resp.Header.Get("X-Hook-Outcome"))
		}
		return d
	}
	for range 50 { // wait for readiness and warm the pool
		time.Sleep(10 * time.Millisecond)
		if r, err := http.Get(strings.Replace(url, "/v1/hooks/checkout.discount/invoke", "/readyz", 1)); err == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				break
			}
		}
	}
	for range 200 {
		invoke()
	}

	pct := func(ds []time.Duration, p float64) time.Duration {
		s := slices.Clone(ds)
		slices.Sort(s)
		return s[int(float64(len(s)-1)*p)].Round(time.Microsecond)
	}

	seq := make([]time.Duration, 3000)
	for i := range seq {
		seq[i] = invoke()
	}
	t.Logf("S12 sequential n=%d  p50=%v p90=%v p99=%v max=%v", len(seq), pct(seq, .5), pct(seq, .9), pct(seq, .99), slices.Max(seq).Round(time.Microsecond))

	// Concurrency 4 matches the tenant's concurrency limit in the snapshot.
	const workers, perWorker = 4, 750
	var mu sync.Mutex
	var par []time.Duration
	var wg sync.WaitGroup
	start := time.Now()
	for range workers {
		wg.Go(func() {
			local := make([]time.Duration, perWorker)
			for i := range local {
				local[i] = invoke()
			}
			mu.Lock()
			par = append(par, local...)
			mu.Unlock()
		})
	}
	wg.Wait()
	elapsed := time.Since(start)
	t.Logf("S12 concurrent workers=%d n=%d  p50=%v p90=%v p99=%v  throughput=%.0f req/s",
		workers, len(par), pct(par, .5), pct(par, .9), pct(par, .99), float64(len(par))/elapsed.Seconds())
}
