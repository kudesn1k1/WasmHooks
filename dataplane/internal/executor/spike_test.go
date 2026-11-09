package executor

import (
	"context"
	"flag"
	"fmt"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/pool"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/slots"
)

// Spike S7b with admission control (spec M2 §9.2). Run with:
//
//	go test -run Spike -v ./internal/executor/ -args -spike
var spike = flag.Bool("spike", false, "run spike measurements")

const (
	spikeLimit    = 50 * time.Millisecond
	spikeDeadline = spikeLimit + 100*time.Millisecond // the gateway's default deadline
	spikeWindow   = 2 * time.Second                   // how long each configuration is saturated
	spikeBaseline = 300                               // merchant-a calls on a quiet executor
)

// TestSpikeAdmissionUnderSaturation is S7b one layer above the runtime:
// through the executor with its pool and running slots. merchant-b keeps N
// infinite-loop calls going for spikeWindow while merchant-a calls every
// millisecond. One run compares slots on (the defaults) with no admission
// control (nil: not M1 either, which still capped instances per pool key)
// on the same machine. Overshoot is the script's own time past its limit
// (ExecNanos), so waiting for a slot does not count as overshoot; the time
// a refused or queued call spent shows in its outcome instead. With one
// noisy tenant its share keeps it to one running script; all slots busy is
// TestSpikeAllSlotsSpinning.
func TestSpikeAdmissionUnderSaturation(t *testing.T) {
	if !*spike {
		t.Skip("spike measurement; run with -spike")
	}
	procs := runtime.GOMAXPROCS(0)
	on := slots.New(slots.Options{})
	t.Logf("S7b+M2 GOMAXPROCS=%d  slots=%d tenant_share=%d queue=%d  hook limit=%v deadline=%v window=%v",
		procs, on.Capacity(), on.TenantShare(), on.Queue(), spikeLimit, spikeDeadline, spikeWindow)

	base := spikeEnv(t, on, 0)
	t.Logf("S7b+M2 baseline            merchant-a %s", base.callsA(spikeBaseline))

	for _, n := range []int{procs, 4 * procs} {
		for _, mode := range []string{"slots on ", "slots off"} {
			var sl *slots.Limiter
			if mode == "slots on " {
				sl = slots.New(slots.Options{})
			}
			e := spikeEnv(t, sl, 0)
			b := e.saturate(map[string]int{"b": n})
			a := e.forA(spikeWindow)
			t.Logf("S7b+M2 spinning=%3d %s merchant-a %s | merchant-b %s", n, mode, a, b.stop())
		}
	}
}

// TestSpikeAllSlotsSpinning checks the rule behind the default capacity
// GOMAXPROCS-1 (decision D30): every slot runs a spinning script, and the
// overshoot of their timeouts is compared between a capacity that leaves a P
// free for the deadline watcher and one that does not. Each noisy tenant has
// a share of one slot and four calls going, so all slots stay busy.
func TestSpikeAllSlotsSpinning(t *testing.T) {
	if !*spike {
		t.Skip("spike measurement; run with -spike")
	}
	procs := runtime.GOMAXPROCS(0)
	for _, c := range []int{max(1, procs-1), procs} {
		sl := slots.New(slots.Options{Capacity: c, TenantShare: 1, Queue: 8 * c})
		e := spikeEnv(t, sl, c)
		load := map[string]int{}
		for i := range c {
			load[noisy(i)] = 4
		}
		b := e.saturate(load)
		a := e.forA(spikeWindow)
		t.Logf("S7b+M2 all slots spinning GOMAXPROCS=%d slots=%d  noisy %s | merchant-a %s", procs, c, b.stop(), a)
	}
}

func noisy(i int) string { return fmt.Sprintf("b%d", i) }

type spikeRun struct{ *env }

// spikeEnv is an executor serving merchant-a (discount), merchant-b and
// noisy tenants b0..b{extra-1} (infinite loop) on a hook with a 50ms limit.
func spikeEnv(t *testing.T, sl *slots.Limiter, extra int) spikeRun {
	t.Helper()
	bindings := map[string]binding{
		"a": {fixture: "discount", config: map[string]string{"threshold": "100", "percent": "10"}},
		"b": {fixture: "infinite-loop"},
	}
	for i := range extra {
		bindings[noisy(i)] = binding{fixture: "infinite-loop"}
	}
	e := newEnvWithSlots(t, bindings, anyOut, pool.Options{GlobalMax: 1024}, sl)
	snap := *e.snap
	snap.Version++
	snap.Hooks = slices.Clone(snap.Hooks)
	snap.Hooks[0].DefVersion++
	snap.Hooks[0].TimeoutMS = spikeLimit.Milliseconds()
	if err := e.cfg.Update(&snap); err != nil {
		t.Fatal(err)
	}
	if err := e.exec.Preload(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := spikeRun{e}
	for range 50 { // warm merchant-a's pool
		r.call("a", discountIn)
	}
	return r
}

func (e spikeRun) call(tenant string, payload []byte) (execproto.ExecuteResult, time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), spikeDeadline)
	defer cancel()
	start := time.Now()
	res, _ := e.exec.Execute(ctx, e.req(tenant, payload))
	return res, time.Since(start)
}

// callsA makes n sequential merchant-a calls.
func (e spikeRun) callsA(n int) string {
	lat := make([]time.Duration, 0, n)
	outcomes := map[string]int{}
	for range n {
		res, d := e.call("a", discountIn)
		lat = append(lat, d)
		outcomes[outcomeKey(res)]++
	}
	return latencies(lat, outcomes)
}

// forA calls merchant-a every millisecond for the window.
func (e spikeRun) forA(window time.Duration) string {
	var lat []time.Duration
	outcomes := map[string]int{}
	for end := time.Now().Add(window); time.Now().Before(end); {
		res, d := e.call("a", discountIn)
		lat = append(lat, d)
		outcomes[outcomeKey(res)]++
		time.Sleep(time.Millisecond)
	}
	return latencies(lat, outcomes)
}

func latencies(lat []time.Duration, outcomes map[string]int) string {
	return fmt.Sprintf("n=%d p50=%v p99=%v max=%v %s",
		len(lat), pct(lat, .5), pct(lat, .99), slices.Max(lat).Round(time.Microsecond), counts(outcomes))
}

type saturation struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	over   []time.Duration // past the limit, timeouts by the hook's own limit
	counts map[string]int
}

// saturate keeps calls[tenant] infinite-loop calls of each tenant going. A
// call refused without running is retried after 1ms, like a client that
// backs off a little.
func (e spikeRun) saturate(calls map[string]int) *saturation {
	ctx, cancel := context.WithCancel(context.Background())
	s := &saturation{cancel: cancel, counts: map[string]int{}}
	for tenant, n := range calls {
		for range n {
			s.wg.Go(func() {
				for ctx.Err() == nil {
					res, _ := e.call(tenant, nil)
					s.mu.Lock()
					s.counts[outcomeKey(res)]++
					if res.Outcome == execproto.OutcomeTimeout && res.Reason == "" {
						s.over = append(s.over, time.Duration(res.ExecNanos)-spikeLimit)
					}
					s.mu.Unlock()
					if res.Outcome == execproto.OutcomeUnavailable {
						time.Sleep(time.Millisecond)
					}
				}
			})
		}
	}
	time.Sleep(20 * time.Millisecond) // let the spinners occupy their slots
	return s
}

func (s *saturation) stop() string {
	s.cancel()
	s.wg.Wait()
	if len(s.over) == 0 {
		return "no timeouts " + counts(s.counts)
	}
	return fmt.Sprintf("overshoot n=%d p50=%v p99=%v max=%v %s", len(s.over), pct(s.over, .5), pct(s.over, .99),
		slices.Max(s.over).Round(time.Microsecond), counts(s.counts))
}

func outcomeKey(res execproto.ExecuteResult) string {
	if res.Reason == "" {
		return string(res.Outcome)
	}
	return string(res.Outcome) + "/" + res.Reason
}

func counts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", k, m[k])
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func pct(ds []time.Duration, p float64) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[int(float64(len(s)-1)*p)].Round(time.Microsecond)
}
