package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Spike S12: end-to-end latency of a warm invocation through the real
// binary (HTTP, gateway, input and output schema checks, quotas, slots,
// executor, pool, Extism). Run with:
//
//	go test -run Spike -v ./cmd/dataplane/ -args -spike

var spike = flag.Bool("spike", false, "run spike measurements")

const spikePayload = `{"cart_total":100,"customer":{"id":"c1","lifetime_spend":1500}}`

// spikeServer is the data plane binary serving the writeEnv snapshot plus
// discount tenants t0..t{n-1}.
type spikeServer struct {
	t      *testing.T
	url    string
	client *http.Client
}

// startSpikeServer runs the binary with extra flags until the test ends and
// warms the pools of merchant-a and of the extra tenants.
func startSpikeServer(t *testing.T, tenants int, extra ...string) *spikeServer {
	t.Helper()
	snapshot, modules := writeEnv(t)
	addDiscountTenants(t, snapshot, tenants)
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
		s.invoke("merchant-a")
	}
	for i := range tenants {
		for range 50 {
			s.invoke(extraTenant(i))
		}
	}
	return s
}

func extraTenant(i int) string { return fmt.Sprintf("t%d", i) }

// addDiscountTenants binds n more tenants to merchant-a's discount module.
func addDiscountTenants(t *testing.T, snapshot string, n int) {
	t.Helper()
	if n == 0 {
		return
	}
	raw, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var snap map[string]any
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	var discount map[string]any
	for _, b := range snap["bindings"].([]any) {
		if b := b.(map[string]any); b["tenant_id"] == "merchant-a" {
			discount = b
		}
	}
	tenants, bindings := snap["tenants"].([]any), snap["bindings"].([]any)
	for i := range n {
		id := extraTenant(i)
		tenants = append(tenants, map[string]any{"external_id": id, "concurrency_limit": 4})
		bindings = append(bindings, map[string]any{"tenant_id": id, "hook": discount["hook"], "module_hash": discount["module_hash"],
			"config": discount["config"], "config_version": 1})
	}
	snap["tenants"], snap["bindings"] = tenants, bindings
	if raw, err = json.Marshal(snap); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (s *spikeServer) invoke(tenant string) time.Duration {
	body := `{"tenant_id":"` + tenant + `","payload":` + spikePayload + `}`
	req, _ := http.NewRequest(http.MethodPost, s.url, strings.NewReader(body))
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
		s.t.Fatalf("%s: outcome %q", tenant, resp.Header.Get("X-Hook-Outcome"))
	}
	return d
}

// concurrent makes perWorker calls from each of len(tenants) parallel
// clients, client i calling as tenants[i], and returns the latencies and
// the throughput.
func (s *spikeServer) concurrent(tenants []string, perWorker int) ([]time.Duration, float64) {
	var mu sync.Mutex
	var all []time.Duration
	var wg sync.WaitGroup
	start := time.Now()
	for _, tenant := range tenants {
		wg.Go(func() {
			local := make([]time.Duration, perWorker)
			for i := range local {
				local[i] = s.invoke(tenant)
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

func repeat(tenant string, n int) []string { return slices.Repeat([]string{tenant}, n) }

func TestSpikeInvokeLatency(t *testing.T) {
	if !*spike {
		t.Skip("spike measurement; run with -spike")
	}
	s := startSpikeServer(t, 0)

	seq := make([]time.Duration, 3000)
	for i := range seq {
		seq[i] = s.invoke("merchant-a")
	}
	t.Logf("S12 sequential n=%d  p50=%v p90=%v p99=%v max=%v", len(seq), pct(seq, .5), pct(seq, .9), pct(seq, .99), slices.Max(seq).Round(time.Microsecond))

	// Four clients of one tenant: above its share of running slots on 4
	// vCPU, so its calls queue in the share (ADR 0005).
	const workers, perWorker = 4, 750
	par, rps := s.concurrent(repeat("merchant-a", workers), perWorker)
	t.Logf("S12 concurrent one tenant workers=%d n=%d  p50=%v p90=%v p99=%v  throughput=%.0f req/s",
		workers, len(par), pct(par, .5), pct(par, .9), pct(par, .99), rps)
}

// TestSpikeSLO measures the data plane SLO in its condition after M2
// (docs/spike-results.md): several tenants call at once, one call each, so
// every tenant stays within its share of running slots. Clients: as many as
// slots, then as many as cores.
func TestSpikeSLO(t *testing.T) {
	if !*spike {
		t.Skip("spike measurement; run with -spike")
	}
	procs := runtime.GOMAXPROCS(0)
	const perWorker = 750
	s := startSpikeServer(t, procs)
	for _, workers := range slices.Compact([]int{max(1, procs-1), procs}) {
		tenants := make([]string, workers)
		for i := range tenants {
			tenants[i] = extraTenant(i)
		}
		par, rps := s.concurrent(tenants, perWorker)
		t.Logf("S12 SLO tenants=%d, one call each  n=%d  p50=%v p90=%v p99=%v  throughput=%.0f req/s",
			workers, len(par), pct(par, .5), pct(par, .9), pct(par, .99), rps)
	}
}

// TestSpikeTenantShareCost measures what the tenant share of running slots
// costs a single tenant (spec M2 §11, ADR 0005): four clients of one tenant
// with the default share against a share of every slot, on the same machine.
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
		s := startSpikeServer(t, 0, c.flags...)
		par, rps := s.concurrent(repeat("merchant-a", workers), perWorker)
		t.Logf("S12 share cost %-17s workers=%d n=%d  p50=%v p90=%v p99=%v  throughput=%.0f req/s",
			c.name, workers, len(par), pct(par, .5), pct(par, .9), pct(par, .99), rps)
	}
}
