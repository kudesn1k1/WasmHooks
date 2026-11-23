package gateway

import (
	"maps"
	"sync"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
)

// Metrics counts completed logical Invoke calls independently of Execute
// attempts. Registered tenants have separate counters for the lifetime of
// their registration; arbitrary input tenant IDs share one counter.
type Metrics struct {
	Outcomes          map[string]map[string]uint64 `json:"outcomes"`
	Unregistered      uint64                       `json:"unregistered"`
	BreakerRejections uint64                       `json:"breaker_rejections"`
	InFlight          int                          `json:"in_flight"`
}

type counters struct {
	mu                              sync.Mutex
	version                         int64
	outcomes                        map[string]map[string]uint64
	unregistered, breakerRejections uint64
}

func (c *counters) prune(view *config.View) {
	if view == nil || c.version == view.Version() {
		return
	}
	maps.DeleteFunc(c.outcomes, func(id string, _ map[string]uint64) bool {
		_, ok := view.Tenant(id)
		return !ok
	})
	c.version = view.Version()
}

func (g *Gateway) record(tenant string, resp Response, err error) {
	c := &g.metrics
	c.mu.Lock()
	defer c.mu.Unlock()
	view := g.cfg.Current()
	c.prune(view)
	if _, ok := view.Tenant(tenant); !ok {
		c.unregistered++
		return
	}
	if c.outcomes[tenant] == nil {
		c.outcomes[tenant] = make(map[string]uint64)
	}
	outcome := string(resp.Outcome)
	if err == ErrExecutionUnknown {
		outcome = "unknown"
	}
	c.outcomes[tenant][outcome]++
	if resp.Reason == reasonBreakerOpen {
		c.breakerRejections++
	}
}

// Metrics returns a copy safe for serialization on the internal listener.
func (g *Gateway) Metrics() Metrics {
	c := &g.metrics
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(g.cfg.Current())
	outcomes := make(map[string]map[string]uint64, len(c.outcomes))
	for id, counts := range c.outcomes {
		outcomes[id] = maps.Clone(counts)
	}
	return Metrics{Outcomes: outcomes, Unregistered: c.unregistered,
		BreakerRejections: c.breakerRejections, InFlight: len(g.inFlight)}
}
