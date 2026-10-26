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

const spikeBody = `{"tenant_id":"merchant-a","payload":{"cart_total":100,"customer":{"id":"c1","lifetime_spend":1500}}}`

// spikeServer is the data plane binary serving the writeEnv snapshot.
type spikeServer struct {
	t      *testing.T
	url    string
	client *http.Client
}

// startSpikeServer runs the binary with extra flags until the test ends and
// warms merchant-a's pool.
func startSpikeServer(t *testing.T, extra ...string) *spikeServer {
	t.Helper()
	snapshot, modules := writeEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pr, pw := io.Pipe()
	args := append([]string{"-listen", "127.0.0.1:0", "-snapshot", snapshot, "-modules-dir", modules, "-log-level", "error"}, extra...)
	go run(ctx, args, pw)
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, pr)
	base := "http://" + strings.TrimSpace(strings.TrimPrefix(line, "listening on "))
	s := &spikeServer{t: t, url: base + "/v1/hooks/checkout.discount/invoke",
		client: &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64}}}
	for range 50 { // wait for readiness
		time.Sleep(10 * time.Millisecond)
		if r, err := http.Get(base + "/readyz"); err == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				break
			}
		}
	}
	for range 200 {
		s.invoke()
	}
	return s
}

func (s *spikeServer) invoke() time.Duration {
	req, _ := http.NewRequest(http.MethodPost, s.url, strings.NewReader(spikeBody))
	req.Header.Set("Authorization", "Bearer "+e2eToken)
	start := time.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	d := time.Since(start)
	if resp.Header.Get("X-Hook-Outcome") != "ok" {
		s.t.Fatalf("outcome %q", resp.Header.Get("X-Hook-Outcome"))
	}
	return d
}

// concurrent makes workers×perWorker calls from parallel clients and
// returns the latencies and the throughput.
func (s *spikeServer) concurrent(workers, perWorker int) ([]time.Duration, float64) {
	var mu sync.Mutex
	var all []time.Duration
	var wg sync.WaitGroup
	start := time.Now()
	for range workers {
		wg.Go(func() {
			local := make([]time.Duration, perWorker)
			for i := range local {
				local[i] = s.invoke()
			}
			mu.Lock()
			all = append(all, local...)
			mu.Unlock()
		})
	}
	wg.Wait()
	return all, float64(len(all)) / time.Since(start).Seconds()
}

func pct(ds []time.Duration, p float64) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[int(float64(len(s)-1)*p)].Round(time.Microsecond)
}

func TestSpikeInvokeLatency(t *testing.T) {
	if !*spike {
		t.Skip("spike measurement; run with -spike")
	}
	s := startSpikeServer(t)

	seq := make([]time.Duration, 3000)
	for i := range seq {
		seq[i] = s.invoke()
	}
	t.Logf("S12 sequential n=%d  p50=%v p90=%v p99=%v max=%v", len(seq), pct(seq, .5), pct(seq, .9), pct(seq, .99), slices.Max(seq).Round(time.Microsecond))

	// Concurrency 4 matches the tenant's concurrency limit in the snapshot.
	const workers, perWorker = 4, 750
	par, rps := s.concurrent(workers, perWorker)
	t.Logf("S12 concurrent workers=%d n=%d  p50=%v p90=%v p99=%v  throughput=%.0f req/s",
		workers, len(par), pct(par, .5), pct(par, .9), pct(par, .99), rps)
}

// TestSpikeTenantShareCost measures what the tenant share of running slots
// costs a single tenant (spec M2 §11, ADR 0005): S12's concurrent load with
// the default share against a share of every slot, on the same machine.
func TestSpikeTenantShareCost(t *testing.T) {
	if !*spike {
		t.Skip("spike measurement; run with -spike")
	}
	const workers, perWorker = 4, 750
	for _, c := range []struct {
		name  string
		flags []string
	}{
		{"default share", nil},
		{"share = all slots", []string{"-exec-tenant-share", "1024"}}, // capped at the slot count
	} {
		s := startSpikeServer(t, c.flags...)
		par, rps := s.concurrent(workers, perWorker)
		t.Logf("S12 share cost %-17s workers=%d n=%d  p50=%v p90=%v p99=%v  throughput=%.0f req/s",
			c.name, workers, len(par), pct(par, .5), pct(par, .9), pct(par, .99), rps)
	}
}
