package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
)

const (
	token    = "whk_test_token"
	hookName = "checkout.discount"
	hash     = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
)

// fakeExecutor records requests and returns a canned result. With block
// set, each call waits for a value on it before returning.
type fakeExecutor struct {
	mu        sync.Mutex
	reqs      []execproto.ExecuteRequest
	deadlines []time.Duration
	res       execproto.ExecuteResult
	err       error
	block     chan struct{}
}

func (f *fakeExecutor) Execute(ctx context.Context, req execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	if d, ok := ctx.Deadline(); ok {
		f.deadlines = append(f.deadlines, time.Until(d))
	}
	res, err, block := f.res, f.err, f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	return res, err
}

func (f *fakeExecutor) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func tokenHash(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func testSnapshot() *config.Snapshot {
	return &config.Snapshot{
		Version: 3,
		APIKeys: []config.APIKey{{ID: "k1", Name: "test", SHA256: tokenHash(token)}},
		Hooks: []config.HookDef{{
			Name: hookName, DefVersion: 7,
			InputSchema:  json.RawMessage(`{"type":"object","required":["cart_total"],"properties":{"cart_total":{"type":"number"}}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
			TimeoutMS:    50, MemoryMaxPages: 64,
		}},
		Tenants: []config.Tenant{{ExternalID: "a"}, {ExternalID: "idle"}},
		Bindings: []config.Binding{{
			TenantID: "a", Hook: hookName, ModuleHash: hash, Config: map[string]string{}, ConfigVersion: 5,
		}},
	}
}

func newGateway(t *testing.T, exec *fakeExecutor, loaded bool) *Gateway {
	t.Helper()
	cfg := config.NewStore()
	if loaded {
		if err := cfg.Update(testSnapshot()); err != nil {
			t.Fatal(err)
		}
	}
	return New(cfg, exec, schema.NewCache(), Options{})
}

var validPayload = []byte(`{"cart_total":10}`)

func TestInvokeUnknownHook(t *testing.T) {
	exec := &fakeExecutor{}
	_, err := newGateway(t, exec, true).Invoke(context.Background(), Request{Hook: "nope", TenantID: "a", Payload: validPayload})
	if !errors.Is(err, ErrUnknownHook) {
		t.Fatalf("want ErrUnknownHook, got %v", err)
	}
}

func TestInvokeInvalidPayload(t *testing.T) {
	exec := &fakeExecutor{}
	_, err := newGateway(t, exec, true).Invoke(context.Background(), Request{Hook: hookName, TenantID: "a", Payload: []byte(`{"cart_total":"ten"}`)})
	if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), "cart_total") {
		t.Fatalf("want ErrInvalidPayload naming the field, got %v", err)
	}
	if exec.calls() != 0 {
		t.Fatal("executor must not be called for an invalid payload")
	}
}

func TestInvokeNoHandler(t *testing.T) {
	for _, tenant := range []string{"unregistered", "idle"} {
		t.Run(tenant, func(t *testing.T) {
			exec := &fakeExecutor{}
			resp, err := newGateway(t, exec, true).Invoke(context.Background(), Request{Hook: hookName, TenantID: tenant, Payload: validPayload})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Outcome != execproto.OutcomeNoHandler || resp.ModuleHash != "" {
				t.Fatalf("resp = %+v", resp)
			}
			if exec.calls() != 0 {
				t.Fatal("executor must not be called without a binding")
			}
		})
	}
}

func TestInvokePinsVersions(t *testing.T) {
	exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeOK, Result: []byte(`{"x":1}`)}}
	resp, err := newGateway(t, exec, true).Invoke(context.Background(), Request{
		Hook: hookName, TenantID: "a", Payload: validPayload, IdempotencyKey: "order-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := exec.reqs[0]
	want := execproto.ExecuteRequest{
		TenantID: "a", Hook: hookName, HookDefVersion: 7, ModuleHash: hash, ConfigVersion: 5,
		Payload: validPayload, IdempotencyKey: "order-1",
	}
	if got.TenantID != want.TenantID || got.HookDefVersion != want.HookDefVersion || got.ModuleHash != want.ModuleHash ||
		got.ConfigVersion != want.ConfigVersion || string(got.Payload) != string(want.Payload) || got.IdempotencyKey != want.IdempotencyKey {
		t.Fatalf("request = %+v, want %+v", got, want)
	}
	if resp.Outcome != execproto.OutcomeOK || string(resp.Result) != `{"x":1}` || resp.ModuleHash != hash {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestInvokeDeadlines(t *testing.T) {
	cases := []struct {
		name     string
		deadline time.Duration
		min, max time.Duration
	}{
		{"default is hook timeout plus extra", 0, 140 * time.Millisecond, 150 * time.Millisecond},
		{"explicit", 5 * time.Second, 4900 * time.Millisecond, 5 * time.Second},
		{"clamped", time.Hour, 29 * time.Second, 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeOK}}
			if _, err := newGateway(t, exec, true).Invoke(context.Background(), Request{Hook: hookName, TenantID: "a", Payload: validPayload, Deadline: tc.deadline}); err != nil {
				t.Fatal(err)
			}
			if d := exec.deadlines[0]; d < tc.min || d > tc.max {
				t.Fatalf("deadline %v not in [%v, %v]", d, tc.min, tc.max)
			}
		})
	}
}

func TestInvokeTransportError(t *testing.T) {
	exec := &fakeExecutor{err: errors.New("connection refused")}
	resp, err := newGateway(t, exec, true).Invoke(context.Background(), Request{Hook: hookName, TenantID: "a", Payload: validPayload})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Outcome != execproto.OutcomeUnavailable || resp.Reason != execproto.ReasonInternal || resp.ModuleHash != "" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestInvokeModuleHashOnlyWhenScriptRan(t *testing.T) {
	cases := []struct {
		res  execproto.ExecuteResult
		want bool
	}{
		{execproto.ExecuteResult{Outcome: execproto.OutcomeOK}, true},
		{execproto.ExecuteResult{Outcome: execproto.OutcomeTimeout}, true},
		{execproto.ExecuteResult{Outcome: execproto.OutcomeHandlerError, Reason: execproto.ReasonTrap}, true},
		{execproto.ExecuteResult{Outcome: execproto.OutcomeHandlerError, Reason: execproto.ReasonInvalidModule}, false},
		{execproto.ExecuteResult{Outcome: execproto.OutcomeUnavailable}, false},
	}
	for _, tc := range cases {
		exec := &fakeExecutor{res: tc.res}
		resp, _ := newGateway(t, exec, true).Invoke(context.Background(), Request{Hook: hookName, TenantID: "a", Payload: validPayload})
		if (resp.ModuleHash != "") != tc.want {
			t.Errorf("%s/%s: module hash %q", tc.res.Outcome, tc.res.Reason, resp.ModuleHash)
		}
	}
}

func TestNotReady(t *testing.T) {
	g := newGateway(t, &fakeExecutor{}, false)
	if _, err := g.Invoke(context.Background(), Request{Hook: hookName, TenantID: "a", Payload: validPayload}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("want ErrNotReady, got %v", err)
	}
	if g.Authenticate(token) {
		t.Fatal("must not authenticate without config")
	}
}

func TestAuthenticate(t *testing.T) {
	g := newGateway(t, &fakeExecutor{}, true)
	if !g.Authenticate(token) {
		t.Fatal("valid token rejected")
	}
	for _, bad := range []string{"", "whk_other", token + " "} {
		if g.Authenticate(bad) {
			t.Fatalf("token %q accepted", bad)
		}
	}
}

// newQuotaGateway serves tenant "a" with the given quotas.
func newQuotaGateway(t *testing.T, exec *fakeExecutor, tenant config.Tenant) (*Gateway, *config.Store) {
	t.Helper()
	snap := testSnapshot()
	tenant.ExternalID = "a"
	snap.Tenants = []config.Tenant{tenant}
	cfg := config.NewStore()
	if err := cfg.Update(snap); err != nil {
		t.Fatal(err)
	}
	return New(cfg, exec, schema.NewCache(), Options{}), cfg
}

func invokeA(g *Gateway, ctx context.Context) Response {
	resp, err := g.Invoke(ctx, Request{Hook: hookName, TenantID: "a", Payload: validPayload})
	if err != nil {
		panic(err)
	}
	return resp
}

func TestInvokeRateLimit(t *testing.T) {
	exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeOK, Result: []byte(`{}`)}}
	g, _ := newQuotaGateway(t, exec, config.Tenant{RateLimitRPS: 2})
	for i := range 2 {
		if r := invokeA(g, context.Background()); r.Outcome != execproto.OutcomeOK {
			t.Fatalf("call %d: outcome %s", i, r.Outcome)
		}
	}
	r := invokeA(g, context.Background())
	if r.Outcome != execproto.OutcomeQuotaExceeded || r.Reason != execproto.ReasonRateLimit || r.ModuleHash != "" {
		t.Fatalf("got %+v, want quota_exceeded/rate_limit without module_hash", r)
	}
	if exec.calls() != 2 {
		t.Fatalf("executor saw %d calls, want 2", exec.calls())
	}
}

func TestInvokeConcurrencyLimit(t *testing.T) {
	exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeOK, Result: []byte(`{}`)}, block: make(chan struct{})}
	g, _ := newQuotaGateway(t, exec, config.Tenant{ConcurrencyLimit: 1})

	first := make(chan Response, 1)
	go func() { first <- invokeA(g, context.Background()) }()
	waitCalls(t, exec, 1)
	second := make(chan Response, 1)
	go func() { second <- invokeA(g, context.Background()) }() // waits in the tenant's queue
	waitQueued(t, g, "a", 1)

	r := invokeA(g, context.Background()) // the queue of length 1 is full
	if r.Outcome != execproto.OutcomeQuotaExceeded || r.Reason != execproto.ReasonConcurrencyLimit {
		t.Fatalf("got %+v, want quota_exceeded/concurrency_limit", r)
	}

	exec.block <- struct{}{}
	if r := <-first; r.Outcome != execproto.OutcomeOK {
		t.Fatalf("first: %+v", r)
	}
	waitCalls(t, exec, 2)
	exec.block <- struct{}{}
	if r := <-second; r.Outcome != execproto.OutcomeOK {
		t.Fatalf("second: %+v", r)
	}
}

func TestInvokeConcurrencyWaitSpendsDeadline(t *testing.T) {
	exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeOK, Result: []byte(`{}`)}, block: make(chan struct{})}
	g, _ := newQuotaGateway(t, exec, config.Tenant{ConcurrencyLimit: 1})
	go invokeA(g, context.Background())
	waitCalls(t, exec, 1)
	defer func() { exec.block <- struct{}{} }()

	resp, err := g.Invoke(context.Background(), Request{Hook: hookName, TenantID: "a", Payload: validPayload, Deadline: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Outcome != execproto.OutcomeQuotaExceeded || resp.Reason != execproto.ReasonConcurrencyLimit {
		t.Fatalf("got %+v, want quota_exceeded/concurrency_limit", resp)
	}
}

func TestInvokeCallerGoneWhileQueued(t *testing.T) {
	exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeOK, Result: []byte(`{}`)}, block: make(chan struct{})}
	g, _ := newQuotaGateway(t, exec, config.Tenant{ConcurrencyLimit: 1})
	go invokeA(g, context.Background())
	waitCalls(t, exec, 1)
	defer func() { exec.block <- struct{}{} }()

	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan Response, 1)
	go func() { got <- invokeA(g, ctx) }()
	waitQueued(t, g, "a", 1)
	cancel()
	if r := <-got; r.Outcome != execproto.OutcomeUnavailable || r.Reason != execproto.ReasonCallerCanceled {
		t.Fatalf("got %+v, want unavailable/caller_canceled", r)
	}
}

// TestInvokeQuotaReleasedOnExecutorError: a transport failure still ends
// the call for the quota.
func TestInvokeQuotaReleasedOnExecutorError(t *testing.T) {
	exec := &fakeExecutor{err: errors.New("connection refused")}
	g, _ := newQuotaGateway(t, exec, config.Tenant{ConcurrencyLimit: 1})
	for range 3 {
		if r := invokeA(g, context.Background()); r.Outcome != execproto.OutcomeUnavailable {
			t.Fatalf("got %+v", r)
		}
	}
	if exec.calls() != 3 {
		t.Fatalf("executor saw %d calls, want 3", exec.calls())
	}
}

func TestInvokeQuotaChangeApplies(t *testing.T) {
	exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeOK, Result: []byte(`{}`)}}
	g, cfg := newQuotaGateway(t, exec, config.Tenant{})
	for range 10 {
		if r := invokeA(g, context.Background()); r.Outcome != execproto.OutcomeOK {
			t.Fatalf("unlimited: %+v", r)
		}
	}
	snap := testSnapshot()
	snap.Version++
	snap.Tenants = []config.Tenant{{ExternalID: "a", RateLimitRPS: 1}}
	if err := cfg.Update(snap); err != nil {
		t.Fatal(err)
	}
	invokeA(g, context.Background())
	if r := invokeA(g, context.Background()); r.Outcome != execproto.OutcomeQuotaExceeded {
		t.Fatalf("after the change: %+v, want quota_exceeded", r)
	}
}

func TestInvokeNoHandlerSkipsQuotas(t *testing.T) {
	exec := &fakeExecutor{}
	g, _ := newQuotaGateway(t, exec, config.Tenant{RateLimitRPS: 1})
	for range 5 {
		resp, err := g.Invoke(context.Background(), Request{Hook: hookName, TenantID: "nobody", Payload: validPayload})
		if err != nil || resp.Outcome != execproto.OutcomeNoHandler {
			t.Fatalf("got %+v, %v", resp, err)
		}
	}
	if r := invokeA(g, context.Background()); r.Outcome == execproto.OutcomeQuotaExceeded {
		t.Fatalf("tenant a was charged for others' calls: %+v", r)
	}
}

func waitCalls(t *testing.T, exec *fakeExecutor, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for exec.calls() < n {
		if time.Now().After(deadline) {
			t.Fatalf("executor saw %d calls, want %d", exec.calls(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitQueued(t *testing.T, g *Gateway, tenant string, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for g.quotas.Waiting(tenant) < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d calls of %s queued, want %d", g.quotas.Waiting(tenant), tenant, n)
		}
		time.Sleep(time.Millisecond)
	}
}
