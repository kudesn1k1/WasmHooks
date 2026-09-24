package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/observe"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/pool"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/extismrt"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/sandboxtest"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
)

const hookName = "checkout.discount"

var (
	inSchema    = json.RawMessage(`{"type":"object"}`)
	discountOut = json.RawMessage(`{"type":"object","required":["discount_percent"],"properties":{"discount_percent":{"type":"integer","minimum":0,"maximum":100}}}`)
	anyOut      = json.RawMessage(`{"type":"object"}`)
	discountIn  = []byte(`{"cart_total":10,"customer":{"id":"c1","lifetime_spend":200}}`)
)

type binding struct {
	fixture string
	config  map[string]string
	limit   int
}

// countingRuntime counts Compile calls.
type countingRuntime struct {
	sandbox.Runtime
	compiles atomic.Int32
}

func (c *countingRuntime) Compile(ctx context.Context, wasm []byte, spec sandbox.ModuleSpec) (sandbox.Module, error) {
	c.compiles.Add(1)
	return c.Runtime.Compile(ctx, wasm, spec)
}

type env struct {
	exec    *Executor
	cfg     *config.Store
	sink    *observe.MemorySink
	pools   *pool.Manager
	rt      *countingRuntime
	dir     string
	snap    *config.Snapshot
	outSpec json.RawMessage
}

func newEnv(t *testing.T, bindings map[string]binding, outSchema json.RawMessage, poolOpts pool.Options) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	snap := &config.Snapshot{
		Version: 1,
		Hooks: []config.HookDef{{
			Name: hookName, DefVersion: 1,
			InputSchema: inSchema, OutputSchema: outSchema,
			TimeoutMS: 100, MemoryMaxPages: 64,
			AllowedEffectTypes: []string{"notify"},
		}},
	}
	for tenant, b := range bindings {
		wasm := sandboxtest.Fixture(t, b.fixture)
		hash := modstore.HashOf(wasm)
		if err := os.WriteFile(filepath.Join(dir, strings.TrimPrefix(hash, "sha256:")+".wasm"), wasm, 0o644); err != nil {
			t.Fatal(err)
		}
		snap.Tenants = append(snap.Tenants, config.Tenant{ExternalID: tenant, ConcurrencyLimit: b.limit})
		snap.Bindings = append(snap.Bindings, config.Binding{
			TenantID: tenant, Hook: hookName, ModuleHash: hash, Config: b.config, ConfigVersion: 1,
		})
	}
	cfg := config.NewStore()
	if err := cfg.Update(snap); err != nil {
		t.Fatal(err)
	}
	base, err := extismrt.New(extismrt.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rt := &countingRuntime{Runtime: base}
	if poolOpts.AcquireTimeout == 0 {
		poolOpts.AcquireTimeout = 20 * time.Millisecond
	}
	pools := pool.NewManager(poolOpts)
	sink := &observe.MemorySink{}
	exec := New(Options{
		Runtime: rt, Modules: modstore.NewFS(dir), Config: cfg, Pools: pools,
		Schemas: schema.NewCache(), Sink: sink, ExecutorID: "test",
	})
	t.Cleanup(func() {
		pools.Close(ctx)
		exec.Close(ctx)
		base.Close(ctx)
	})
	return &env{exec: exec, cfg: cfg, sink: sink, pools: pools, rt: rt, dir: dir, snap: snap, outSpec: outSchema}
}

func (e *env) req(tenant string, payload []byte) execproto.ExecuteRequest {
	v := e.cfg.Current()
	h, _ := v.Hook(hookName)
	b, _ := v.Binding(tenant, hookName)
	return execproto.ExecuteRequest{
		TenantID: tenant, Hook: hookName, HookDefVersion: h.DefVersion,
		ModuleHash: b.ModuleHash, ConfigVersion: b.ConfigVersion, Payload: payload,
	}
}

func (e *env) run(t *testing.T, tenant string, payload []byte) execproto.ExecuteResult {
	t.Helper()
	res, err := e.exec.Execute(context.Background(), e.req(tenant, payload))
	if err != nil {
		t.Fatalf("transport error from in-process executor: %v", err)
	}
	return res
}

func wantOutcome(t *testing.T, res execproto.ExecuteResult, outcome execproto.Outcome, reason string) {
	t.Helper()
	if res.Outcome != outcome || res.Reason != reason {
		t.Fatalf("got %s/%s (%s), want %s/%s", res.Outcome, res.Reason, res.Error, outcome, reason)
	}
}

func TestExecutorOK(t *testing.T) {
	e := newEnv(t, map[string]binding{"a": {fixture: "discount", config: map[string]string{"threshold": "100", "percent": "5"}}}, discountOut, pool.Options{})
	res := e.run(t, "a", discountIn)
	wantOutcome(t, res, execproto.OutcomeOK, "")
	if !strings.Contains(string(res.Result), `"discount_percent":5`) {
		t.Fatalf("result = %s", res.Result)
	}
	if len(res.Logs) != 1 || res.Logs[0] != "info: discount computed: 5" {
		t.Fatalf("logs = %q", res.Logs)
	}
	if !res.ColdStart {
		t.Fatal("first call must be cold")
	}
	if res2 := e.run(t, "a", discountIn); res2.ColdStart {
		t.Fatal("second call must reuse the warm instance")
	}
	recs := e.sink.All()
	if len(recs) != 2 || recs[0].Outcome != "ok" || recs[0].TenantID != "a" || recs[0].ExecutorID != "test" {
		t.Fatalf("records = %+v", recs)
	}
}

func TestExecutorTimeout(t *testing.T) {
	e := newEnv(t, map[string]binding{"b": {fixture: "infinite-loop"}}, anyOut, pool.Options{})
	// Measure the timeout, not the cold compile.
	if err := e.exec.Preload(context.Background()); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := e.run(t, "b", nil)
	wantOutcome(t, res, execproto.OutcomeTimeout, "")
	if d := time.Since(start); d > 350*time.Millisecond {
		t.Fatalf("timeout took %v", d)
	}
	if n := e.pools.Stats().Instances; n != 0 {
		t.Fatalf("timed-out instance kept alive: %d instances", n)
	}
}

func TestExecutorTrap(t *testing.T) {
	// The test hook allows 4 MiB: the bomb traps within a few milliseconds,
	// far below the 100ms timeout (spike S8).
	e := newEnv(t, map[string]binding{"c": {fixture: "memory-bomb"}}, anyOut, pool.Options{})
	res := e.run(t, "c", nil)
	wantOutcome(t, res, execproto.OutcomeHandlerError, execproto.ReasonTrap)
	if n := e.pools.Stats().Instances; n != 0 {
		t.Fatalf("trapped instance kept alive: %d instances", n)
	}
}

func TestExecutorGuestError(t *testing.T) {
	e := newEnv(t, map[string]binding{"g": {fixture: "guest-error"}}, anyOut, pool.Options{})
	res := e.run(t, "g", nil)
	wantOutcome(t, res, execproto.OutcomeHandlerError, execproto.ReasonGuestError)
	if !strings.Contains(res.Error, "guest failure: boom") {
		t.Fatalf("error = %q", res.Error)
	}
}

func TestExecutorInvalidOutput(t *testing.T) {
	e := newEnv(t, map[string]binding{"x": {fixture: "bad-output"}}, discountOut, pool.Options{})
	res := e.run(t, "x", nil)
	// bad-output violates both the effect allowlist and the output schema;
	// effects are checked first.
	wantOutcome(t, res, execproto.OutcomeHandlerError, execproto.ReasonInvalidEffect)
	if s := e.pools.Stats(); s.Idle != 1 {
		t.Fatalf("instance with bad output must return to the pool: %+v", s)
	}
}

func TestExecutorOutputSchemaViolation(t *testing.T) {
	strict := json.RawMessage(`{"type":"object","required":["discount_percent"],"properties":{"discount_percent":{"type":"string"}}}`)
	e := newEnv(t, map[string]binding{"a": {fixture: "discount"}}, strict, pool.Options{})
	res := e.run(t, "a", discountIn)
	wantOutcome(t, res, execproto.OutcomeHandlerError, execproto.ReasonInvalidOutput)
	if !strings.Contains(res.Error, "discount_percent") {
		t.Fatalf("error does not point at the field: %q", res.Error)
	}
	if res.Result != nil {
		t.Fatal("result must be empty on failure")
	}
}

func TestExecutorInvalidModule(t *testing.T) {
	e := newEnv(t, map[string]binding{"h": {fixture: "http-call"}}, anyOut, pool.Options{})
	res := e.run(t, "h", nil)
	wantOutcome(t, res, execproto.OutcomeHandlerError, execproto.ReasonInvalidModule)
	e.run(t, "h", nil)
	if n := e.rt.compiles.Load(); n != 1 {
		t.Fatalf("invalid module compiled %d times, want 1 (cached)", n)
	}
}

func TestExecutorModuleMissing(t *testing.T) {
	e := newEnv(t, map[string]binding{"a": {fixture: "discount"}}, discountOut, pool.Options{})
	req := e.req("a", discountIn)
	os.Remove(filepath.Join(e.dir, strings.TrimPrefix(req.ModuleHash, "sha256:")+".wasm"))
	res, _ := e.exec.Execute(context.Background(), req)
	wantOutcome(t, res, execproto.OutcomeUnavailable, execproto.ReasonModuleFetch)
}

func TestExecutorStaleConfig(t *testing.T) {
	e := newEnv(t, map[string]binding{"a": {fixture: "discount"}}, discountOut, pool.Options{})
	for name, mutate := range map[string]func(*execproto.ExecuteRequest){
		"hook version":   func(r *execproto.ExecuteRequest) { r.HookDefVersion++ },
		"config version": func(r *execproto.ExecuteRequest) { r.ConfigVersion++ },
		"tenant":         func(r *execproto.ExecuteRequest) { r.TenantID = "ghost" },
	} {
		t.Run(name, func(t *testing.T) {
			req := e.req("a", discountIn)
			mutate(&req)
			res, _ := e.exec.Execute(context.Background(), req)
			wantOutcome(t, res, execproto.OutcomeUnavailable, execproto.ReasonStaleConfig)
		})
	}
}

func TestExecutorDeadlineBeforeStart(t *testing.T) {
	e := newEnv(t, map[string]binding{"n": {fixture: "counter"}}, anyOut, pool.Options{})
	if err := e.exec.Preload(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, _ := e.exec.Execute(ctx, e.req("n", nil))
	wantOutcome(t, res, execproto.OutcomeUnavailable, execproto.ReasonDeadline)
	if res := e.run(t, "n", nil); !strings.Contains(string(res.Result), `"count":1`) {
		t.Fatalf("script ran before the deadline check: %s", res.Result)
	}
}

func TestExecutorPoolSaturated(t *testing.T) {
	e := newEnv(t, map[string]binding{"b": {fixture: "infinite-loop", limit: 1}}, anyOut, pool.Options{})
	if err := e.exec.Preload(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { e.run(t, "b", nil); close(done) }()
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	res := e.run(t, "b", nil)
	wantOutcome(t, res, execproto.OutcomeUnavailable, execproto.ReasonPoolSaturated)
	if d := time.Since(start); d > 80*time.Millisecond {
		t.Fatalf("saturation reported after %v", d)
	}
	<-done
}

func TestExecutorTenantIsolationSameModule(t *testing.T) {
	t.Run("state", func(t *testing.T) {
		e := newEnv(t, map[string]binding{"a": {fixture: "counter"}, "b": {fixture: "counter"}}, anyOut, pool.Options{})
		if ra, rb := e.req("a", nil), e.req("b", nil); ra.ModuleHash != rb.ModuleHash {
			t.Fatal("test needs identical modules")
		}
		for want := 1; want <= 3; want++ {
			for _, tenant := range []string{"a", "b"} {
				res := e.run(t, tenant, nil)
				if !strings.Contains(string(res.Result), fmt.Sprintf(`"count":%d`, want)) {
					t.Fatalf("tenant %s call %d: %s", tenant, want, res.Result)
				}
			}
		}
		if n := e.rt.compiles.Load(); n != 1 {
			t.Fatalf("identical module compiled %d times, want 1", n)
		}
	})
	t.Run("config", func(t *testing.T) {
		e := newEnv(t, map[string]binding{
			"a": {fixture: "echo-config", config: map[string]string{"k": "a"}},
			"b": {fixture: "echo-config", config: map[string]string{"k": "b"}},
		}, anyOut, pool.Options{AcquireTimeout: 5 * time.Second}) // isolation, not throughput: every call must run
		var wg sync.WaitGroup
		errs := make(chan string, 50)
		for i := range 50 {
			tenant := []string{"a", "b"}[i%2]
			wg.Go(func() {
				res := e.run(t, tenant, []byte(`{"keys":["k"]}`))
				if !strings.Contains(string(res.Result), `"k":"`+tenant+`"`) {
					errs <- fmt.Sprintf("tenant %s saw %s (%s)", tenant, res.Result, res.Outcome)
				}
			})
		}
		wg.Wait()
		close(errs)
		for msg := range errs {
			t.Error(msg)
		}
	})
}

func TestExecutorTimeoutDoesNotBlockOtherTenant(t *testing.T) {
	e := newEnv(t, map[string]binding{"a": {fixture: "discount"}, "b": {fixture: "infinite-loop"}}, discountOut, pool.Options{})
	if err := e.exec.Preload(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.run(t, "a", discountIn) // warm a

	stop := make(chan struct{})
	var looping sync.WaitGroup
	looping.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				e.run(t, "b", nil)
			}
		}
	})
	time.Sleep(10 * time.Millisecond)
	var worst time.Duration
	for range 20 {
		s := time.Now()
		res := e.run(t, "a", discountIn)
		if d := time.Since(s); d > worst {
			worst = d
		}
		wantOutcome(t, res, execproto.OutcomeOK, "")
	}
	close(stop)
	looping.Wait()
	if worst > 50*time.Millisecond {
		t.Fatalf("tenant a slowed down by b: worst %v", worst)
	}
}

func TestExecutorCompileSurvivesShortDeadline(t *testing.T) {
	e := newEnv(t, map[string]binding{"a": {fixture: "discount"}}, discountOut, pool.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	res, _ := e.exec.Execute(ctx, e.req("a", discountIn))
	wantOutcome(t, res, execproto.OutcomeUnavailable, execproto.ReasonDeadline)

	wantOutcome(t, e.run(t, "a", discountIn), execproto.OutcomeOK, "")
	if n := e.rt.compiles.Load(); n != 1 {
		t.Fatalf("module compiled %d times, want 1", n)
	}
}

func TestExecutorConfigChangeNewPool(t *testing.T) {
	e := newEnv(t, map[string]binding{"a": {fixture: "echo-config", config: map[string]string{"k": "old"}}}, anyOut, pool.Options{})
	in := []byte(`{"keys":["k"]}`)
	if res := e.run(t, "a", in); !strings.Contains(string(res.Result), `"k":"old"`) {
		t.Fatalf("result = %s", res.Result)
	}
	next := *e.snap
	next.Version = 2
	next.Bindings = []config.Binding{e.snap.Bindings[0]}
	next.Bindings[0].Config = map[string]string{"k": "new"}
	next.Bindings[0].ConfigVersion = 2
	if err := e.cfg.Update(&next); err != nil {
		t.Fatal(err)
	}
	if res := e.run(t, "a", in); !strings.Contains(string(res.Result), `"k":"new"`) {
		t.Fatalf("config change not visible: %s", res.Result)
	}
}

func TestExecutorPreload(t *testing.T) {
	e := newEnv(t, map[string]binding{
		"a": {fixture: "discount"}, "a2": {fixture: "discount"}, "n": {fixture: "counter"}, "h": {fixture: "http-call"},
	}, anyOut, pool.Options{})
	err := e.exec.Preload(context.Background())
	if err == nil || !errors.Is(err, sandbox.ErrInvalidModule) || !strings.Contains(err.Error(), "h/") {
		t.Fatalf("preload must report the invalid binding: %v", err)
	}
	if n := e.rt.compiles.Load(); n != 3 {
		t.Fatalf("compiled %d modules, want 3 unique", n)
	}
	wantOutcome(t, e.run(t, "n", nil), execproto.OutcomeOK, "")
	if n := e.rt.compiles.Load(); n != 3 {
		t.Fatal("call after preload recompiled")
	}
}

func TestExecutorConcurrentCompileOnce(t *testing.T) {
	e := newEnv(t, map[string]binding{"a": {fixture: "discount"}}, discountOut, pool.Options{})
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { e.run(t, "a", discountIn) })
	}
	wg.Wait()
	if n := e.rt.compiles.Load(); n != 1 {
		t.Fatalf("compiled %d times, want 1", n)
	}
}

func TestExecutorEffectsReturned(t *testing.T) {
	// discount emits no effects; the envelope must still produce an empty list.
	e := newEnv(t, map[string]binding{"a": {fixture: "discount"}}, discountOut, pool.Options{})
	req := e.req("a", discountIn)
	req.IdempotencyKey = "order-1"
	res, _ := e.exec.Execute(context.Background(), req)
	wantOutcome(t, res, execproto.OutcomeOK, "")
	if len(res.Effects) != 0 {
		t.Fatalf("effects = %+v", res.Effects)
	}
	if recs := e.sink.All(); recs[0].IdempotencyKey != "order-1" {
		t.Fatalf("idempotency key not recorded: %+v", recs[0])
	}
}
