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

// fakeExecutor records requests and returns a canned result.
type fakeExecutor struct {
	mu        sync.Mutex
	reqs      []execproto.ExecuteRequest
	deadlines []time.Duration
	res       execproto.ExecuteResult
	err       error
}

func (f *fakeExecutor) Execute(ctx context.Context, req execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	if d, ok := ctx.Deadline(); ok {
		f.deadlines = append(f.deadlines, time.Until(d))
	}
	return f.res, f.err
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
	for outcome, want := range map[execproto.Outcome]bool{
		execproto.OutcomeOK: true, execproto.OutcomeTimeout: true, execproto.OutcomeHandlerError: true,
		execproto.OutcomeUnavailable: false,
	} {
		exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: outcome}}
		resp, _ := newGateway(t, exec, true).Invoke(context.Background(), Request{Hook: hookName, TenantID: "a", Payload: validPayload})
		if (resp.ModuleHash != "") != want {
			t.Errorf("%s: module hash %q", outcome, resp.ModuleHash)
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
