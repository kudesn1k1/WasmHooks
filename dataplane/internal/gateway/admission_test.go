package gateway

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
)

func TestGlobalLimitIncludesTenantQuotaWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeOK}, block: make(chan struct{})}
		g, cfg := twoTenants(t, exec, Options{MaxInFlight: 2})
		snap := testSnapshot()
		snap.Version++
		snap.Tenants[0].ConcurrencyLimit = 1
		second := snap.Bindings[0]
		second.TenantID = "idle"
		snap.Bindings = append(snap.Bindings, second)
		if err := cfg.Update(snap); err != nil {
			t.Fatal(err)
		}
		done := make(chan Response, 2)
		for range 2 {
			go func() { done <- invokeA(g, t.Context()) }()
		}
		synctest.Wait()
		if exec.calls() != 1 || g.quotas.Waiting("a") != 1 {
			t.Fatal("first tenant did not fill admission and its queue")
		}
		req := Request{TenantID: "idle", Hook: hookName, Payload: validPayload}
		r, err := g.Invoke(t.Context(), req)
		if err != nil || r.Outcome != execproto.OutcomeUnavailable || r.Reason != execproto.ReasonOverloaded || g.Metrics().InFlight != 2 {
			t.Fatalf("overloaded: %+v, %v, metrics %+v", r, err, g.Metrics())
		}
		close(exec.block)
		for range 2 {
			if r := <-done; r.Outcome != execproto.OutcomeOK {
				t.Fatalf("admitted: %+v", r)
			}
		}
		r, err = g.Invoke(t.Context(), req)
		if err != nil || r.Outcome != execproto.OutcomeOK || g.Metrics().InFlight != 0 {
			t.Fatalf("after release: %+v, %v", r, err)
		}
	})
}

func TestMetricsCountLogicalOutcomesIncludingQuotaAndUnknown(t *testing.T) {
	exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeOK}}
	g, _ := newQuotaGateway(t, exec, config.Tenant{RateLimitRPS: 2})
	invokeA(g, t.Context())
	exec.err = errors.New("execution status lost")
	req := Request{TenantID: "a", Hook: hookName, Payload: validPayload}
	_, _ = g.Invoke(t.Context(), req)
	invokeA(g, t.Context())
	for _, tenant := range []string{"unregistered-1", "unregistered-2"} {
		req.TenantID = tenant
		_, _ = g.Invoke(t.Context(), req)
	}
	m := g.Metrics()
	a := m.Outcomes["a"]
	if len(m.Outcomes) != 1 || a["ok"] != 1 || a["unknown"] != 1 || a["quota_exceeded"] != 1 || m.Unregistered != 2 {
		t.Fatalf("logical calls: %+v", m)
	}
}

func TestProbeGuestFailureRestartsCooldownAndInfrastructureFailurePermitsNextProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exec := &fakeExecutor{res: execproto.ExecuteResult{Outcome: execproto.OutcomeHandlerError, Reason: execproto.ReasonGuestError}}
		g, _ := twoTenants(t, exec, Options{BreakerThreshold: 1, BreakerCooldown: time.Minute})
		invokeA(g, t.Context())
		time.Sleep(time.Minute)
		if r := invokeA(g, t.Context()); r.Outcome != execproto.OutcomeHandlerError {
			t.Fatalf("first probe: %+v", r)
		}
		if r := invokeA(g, t.Context()); r.Reason != "breaker_open" {
			t.Fatalf("expected cooldown: %+v", r)
		}
		time.Sleep(time.Minute)
		exec.res = execproto.ExecuteResult{Outcome: execproto.OutcomeUnavailable, Reason: execproto.ReasonOverloaded}
		invokeA(g, t.Context())
		exec.res = execproto.ExecuteResult{Outcome: execproto.OutcomeOK}
		if r := invokeA(g, t.Context()); r.Outcome != execproto.OutcomeOK || g.Metrics().BreakerRejections != 1 {
			t.Fatalf("infrastructure failure stranded the circuit: %+v, metrics %+v", r, g.Metrics())
		}
	})
}
