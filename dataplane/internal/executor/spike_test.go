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
	spikeCalls    = 300
)

// TestSpikeAdmissionUnderSaturation is S7b one layer above the runtime:
// through the executor with its pool and running slots. merchant-b keeps N
// infinite-loop calls going while merchant-a makes sequential warm calls.
// One run compares slots on (the defaults) with slots off (nil: no
// admission control at all, not M1, which still capped instances per pool
// key) on the same machine. Overshoot is the script's own time past its
// limit (ExecNanos), so waiting for a slot does not count as overshoot; the
// time a refused or queued call spent shows in its outcome instead.
func TestSpikeAdmissionUnderSaturation(t *testing.T) {
	if !*spike {
		t.Skip("spike measurement; run with -spike")
	}
	procs := runtime.GOMAXPROCS(0)
	on := slots.New(slots.Options{})
	t.Logf("S7b+M2 GOMAXPROCS=%d  slots=%d tenant_share=%d queue=%d  hook limit=%v deadline=%v",
		procs, on.Capacity(), on.TenantShare(), on.Queue(), spikeLimit, spikeDeadline)

	base := spikeEnv(t, on)
	t.Logf("S7b+M2 baseline           merchant-a %s", base.runA(t))

	for _, n := range []int{procs, 4 * procs} {
		for _, mode := range []string{"slots on ", "slots off"} {
			var sl *slots.Limiter
			if mode == "slots on " {
				sl = slots.New(slots.Options{})
			}
			e := spikeEnv(t, sl)
			b := e.saturate(n)
			a := e.runA(t)
			bres := b.stop()
			t.Logf("S7b+M2 spinning=%3d %s merchant-a %s | merchant-b %s", n, mode, a, bres)
		}
	}
}

type spikeRun struct{ *env }

// spikeEnv is an executor serving merchant-a (discount) and merchant-b
// (infinite loop) on a hook with a 50ms limit.
func spikeEnv(t *testing.T, sl *slots.Limiter) spikeRun {
	t.Helper()
	e := newEnvWithSlots(t, map[string]binding{
		"a": {fixture: "discount", config: map[string]string{"threshold": "100", "percent": "10"}},
		"b": {fixture: "infinite-loop"},
	}, anyOut, pool.Options{GlobalMax: 1024}, sl)
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

// runA makes merchant-a's sequential calls and summarizes them.
func (e spikeRun) runA(t *testing.T) string {
	t.Helper()
	lat := make([]time.Duration, 0, spikeCalls)
	outcomes := map[string]int{}
	for range spikeCalls {
		res, d := e.call("a", discountIn)
		lat = append(lat, d)
		outcomes[outcomeKey(res)]++
	}
	return fmt.Sprintf("p50=%v p99=%v max=%v %s",
		pct(lat, .5), pct(lat, .99), slices.Max(lat).Round(time.Microsecond), counts(outcomes))
}

type saturation struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	over   []time.Duration // past the limit, timeouts by the hook's own limit
	counts map[string]int
}

// saturate keeps n merchant-b calls going. A call refused without running
// is retried after 1ms, like a client that backs off a little.
func (e spikeRun) saturate(n int) *saturation {
	ctx, cancel := context.WithCancel(context.Background())
	s := &saturation{cancel: cancel, counts: map[string]int{}}
	for range n {
		s.wg.Go(func() {
			for ctx.Err() == nil {
				res, _ := e.call("b", nil)
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
	time.Sleep(20 * time.Millisecond) // let the spinners occupy their slots
	return s
}

func (s *saturation) stop() string {
	s.cancel()
	s.wg.Wait()
	if len(s.over) == 0 {
		return "no timeouts " + counts(s.counts)
	}
	return fmt.Sprintf("overshoot p50=%v max=%v %s", pct(s.over, .5), slices.Max(s.over).Round(time.Microsecond), counts(s.counts))
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
