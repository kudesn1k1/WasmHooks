package gateway

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
)

func twoTenants(t *testing.T, exec *fakeExecutor, opts Options) (*Gateway, *config.Store) {
	t.Helper()
	snap := testSnapshot()
	second := snap.Bindings[0]
	second.TenantID = "idle"
	snap.Bindings = append(snap.Bindings, second)
	cfg := config.NewStore()
	if err := cfg.Update(snap); err != nil {
		t.Fatal(err)
	}
	return New(cfg, exec, schema.NewCache(), opts), cfg
}

func TestBreakerIsolatesPairAndAllowsOneProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeTimeout}}
		g, _ := twoTenants(t, exec, Options{})
		for range 5 {
			if r := invokeA(g, t.Context()); r.Outcome != execproto.OutcomeTimeout {
				t.Fatalf("guest timeout: %+v", r)
			}
		}
		assertOpen := func() {
			t.Helper()
			if r := invokeA(g, t.Context()); r.Outcome != execproto.OutcomeUnavailable || r.Reason != "breaker_open" || r.ModuleHash != "" {
				t.Fatalf("expected a refusal before Execute, got %+v", r)
			}
		}
		assertOpen()
		exec.res = execproto.ExecuteResult{Outcome: execproto.OutcomeOK}
		r, err := g.Invoke(t.Context(), Request{TenantID: "idle", Hook: hookName, Payload: validPayload})
		if err != nil || r.Outcome != execproto.OutcomeOK {
			t.Fatalf("other tenant: %+v, %v", r, err)
		}

		time.Sleep(5 * time.Second)
		exec.block = make(chan struct{})
		first := make(chan Response, 1)
		go func() { first <- invokeA(g, context.Background()) }()
		synctest.Wait()
		assertOpen()
		close(exec.block)
		if r := <-first; r.Outcome != execproto.OutcomeOK {
			t.Fatalf("probe: %+v", r)
		}
		if r := invokeA(g, t.Context()); r.Outcome != execproto.OutcomeOK || exec.calls() != 8 {
			t.Fatalf("after probe: %+v, %d calls", r, exec.calls())
		}
	})
}

func TestBreakerIgnoresNodeFailuresAndCallerDeadlines(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  execproto.ExecuteResult
		err  error
	}{
		{"transport", execproto.ExecuteResult{}, errors.New("response lost")},
		{"overload", execproto.ExecuteResult{Outcome: execproto.OutcomeUnavailable, Reason: execproto.ReasonOverloaded}, nil},
		{"short deadline", execproto.ExecuteResult{Outcome: execproto.OutcomeTimeout, Reason: execproto.ReasonCallerDeadline}, nil},
		{"internal", execproto.ExecuteResult{Outcome: execproto.OutcomeHandlerError, Reason: execproto.ReasonInternal}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := &fakeExecutor{res: tc.res, err: tc.err}
			g, _ := twoTenants(t, exec, Options{})
			for range 8 {
				_, _ = g.Invoke(t.Context(), Request{TenantID: "a", Hook: hookName, Payload: validPayload})
			}
			exec.res, exec.err = execproto.ExecuteResult{Outcome: execproto.OutcomeOK}, nil
			if r := invokeA(g, t.Context()); r.Outcome != execproto.OutcomeOK || exec.calls() != 9 {
				t.Fatalf("node/caller failures affected a healthy guest: %+v, %d calls", r, exec.calls())
			}
		})
	}
}

func TestDisableBreakerKeepsSendingGuestTimeouts(t *testing.T) {
	exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeTimeout}}
	g, _ := twoTenants(t, exec, Options{BreakerThreshold: 1, DisableBreaker: true})
	for range 8 {
		if r := invokeA(g, t.Context()); r.Outcome != execproto.OutcomeTimeout {
			t.Fatalf("disabled breaker intercepted the call: %+v", r)
		}
	}
}

func TestBreakerTracksPinnedRevisionAcrossSnapshotChanges(t *testing.T) {
	exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeHandlerError, Reason: execproto.ReasonTrap}}
	g, cfg := twoTenants(t, exec, Options{})
	for range 5 {
		invokeA(g, t.Context())
	}
	snap := testSnapshot()
	snap.Version++
	if err := cfg.Update(snap); err != nil {
		t.Fatal(err)
	}
	if r := invokeA(g, t.Context()); r.Reason != "breaker_open" {
		t.Fatalf("unrelated snapshot change reopened the failing binding: %+v", r)
	}
	snap = testSnapshot()
	snap.Version += 2
	snap.Bindings[0].ConfigVersion++
	if err := cfg.Update(snap); err != nil {
		t.Fatal(err)
	}
	exec.res = execproto.ExecuteResult{Outcome: execproto.OutcomeOK}
	if r := invokeA(g, t.Context()); r.Outcome != execproto.OutcomeOK {
		t.Fatalf("corrected binding is still blocked: %+v", r)
	}
}
