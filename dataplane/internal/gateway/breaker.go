package gateway

import (
	"maps"
	"sync"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
)

const reasonBreakerOpen = "breaker_open"

// Each published revision starts with a fresh breaker. A snapshot changing
// an unrelated pair preserves the original failure history.
type breakerKey struct {
	tenant, hook, hash string
	def, config        int64
}

func (k breakerKey) current(view *config.View) bool {
	b, bound := view.Binding(k.tenant, k.hook)
	h, defined := view.Hook(k.hook)
	return bound && defined && h.DefVersion == k.def && b.ConfigVersion == k.config && b.ModuleHash == k.hash
}

type breaker struct {
	mu      sync.Mutex
	pairs   map[breakerKey]*circuit
	version int64
}

type circuit struct {
	failures int
	retryAt  time.Time
	probing  bool
}

func (g *Gateway) enterCircuit(key breakerKey) (finish func(execproto.ExecuteResult), allowed bool) {
	if g.opts.DisableBreaker {
		return func(execproto.ExecuteResult) {}, true
	}
	b := &g.breaker
	b.mu.Lock()
	defer b.mu.Unlock()
	view := g.cfg.Current()
	if view.Version() != b.version {
		maps.DeleteFunc(b.pairs, func(k breakerKey, _ *circuit) bool { return !k.current(view) })
		b.version = view.Version()
	}
	// This request may still execute its pinned revision after a config
	// change, but its result must never affect the new revision.
	if !key.current(view) {
		return func(execproto.ExecuteResult) {}, true
	}
	c := b.pairs[key]
	if c == nil {
		c = &circuit{}
		b.pairs[key] = c
	}
	if !c.retryAt.IsZero() {
		if time.Now().Before(c.retryAt) || c.probing {
			return nil, false
		}
		c.probing = true
	}
	return func(res execproto.ExecuteResult) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.pairs[key] != c {
			return // an older in-flight request finished after the breaker opened
		}
		c.probing = false
		switch {
		case res.Outcome == execproto.OutcomeOK:
			c.failures = 0
			c.retryAt = time.Time{}
		case guestFailure(res):
			c.failures++
			if c.failures >= g.opts.BreakerThreshold {
				b.pairs[key] = &circuit{failures: c.failures, retryAt: time.Now().Add(g.opts.BreakerCooldown)}
			}
		}
	}, true
}

func guestFailure(res execproto.ExecuteResult) bool {
	if res.Outcome == execproto.OutcomeTimeout {
		return res.Reason == "" // the hook's own execution limit
	}
	if res.Outcome == execproto.OutcomeHandlerError {
		switch res.Reason {
		case execproto.ReasonTrap, execproto.ReasonGuestError, execproto.ReasonInvalidOutput,
			execproto.ReasonInvalidEffect, execproto.ReasonInvalidModule:
			return true
		}
	}
	return false
}
