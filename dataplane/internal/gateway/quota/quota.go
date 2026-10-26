// Package quota enforces tenant quotas on the gateway: a token bucket for
// the call rate and a cap on concurrent calls with a short FIFO queue.
//
// Limits arrive with every call from the current config snapshot, so a
// quota changed in the control plane applies from the next call, without a
// restart. State is per gateway replica: with several replicas a tenant's
// effective quota is the quota times the replicas (main spec §11.3).
package quota

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

var (
	// ErrRateLimited: the tenant's token bucket is empty.
	ErrRateLimited = errors.New("quota: rate limit exceeded")
	// ErrConcurrency: the tenant's concurrency queue is full, or the call's
	// deadline expired while it waited in that queue.
	ErrConcurrency = errors.New("quota: concurrency limit exceeded")
)

// Limits are a tenant's quotas as the snapshot defines them.
type Limits struct {
	RateRPS     int // calls per second, burst max(1, RateRPS); 0: unlimited
	Concurrency int // concurrent calls, also the queue length; below 1 counts as 1
}

// Manager is safe for concurrent use.
type Manager struct {
	now func() time.Time

	mu      sync.Mutex
	tenants map[string]*tenant
}

type tenant struct {
	rateRPS  int
	bucket   *rate.Limiter // nil while the rate is unlimited
	limit    int
	inFlight int
	waiters  []*waiter // FIFO
}

type waiter struct {
	ready chan struct{} // closed under mu when the call is admitted
	done  bool
}

// New creates a manager. now is the clock for token buckets; nil means
// time.Now.
func New(now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{now: now, tenants: make(map[string]*tenant)}
}

// Admit counts one call of tenantID against lim. A call over the rate fails
// at once with ErrRateLimited. A call over the concurrency limit waits in
// the tenant's queue while ctx allows; a full queue or an expired deadline
// fails with ErrConcurrency, a canceled ctx with ctx.Err(). release ends the
// call; only its first call has an effect.
func (m *Manager) Admit(ctx context.Context, tenantID string, lim Limits) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := m.now()
	m.mu.Lock()
	t := m.tenant(tenantID)
	m.applyLocked(t, lim, now)

	if t.bucket != nil && !t.bucket.AllowN(now, 1) {
		m.dropIfIdleLocked(tenantID, t, now)
		m.mu.Unlock()
		return nil, ErrRateLimited
	}
	// Room frees up for the queue first, so with room left nobody waits.
	if t.inFlight < t.limit {
		t.inFlight++
		m.mu.Unlock()
		return m.releaser(tenantID, t), nil
	}
	if len(t.waiters) >= t.limit {
		m.mu.Unlock()
		return nil, ErrConcurrency
	}
	w := &waiter{ready: make(chan struct{})}
	t.waiters = append(t.waiters, w)
	m.mu.Unlock()

	select {
	case <-w.ready:
		return m.releaser(tenantID, t), nil
	case <-ctx.Done():
	}
	m.mu.Lock()
	if w.done {
		// Admitted as ctx ended: pass the room on.
		m.mu.Unlock()
		m.release(tenantID, t)
	} else {
		t.waiters = slices.DeleteFunc(t.waiters, func(o *waiter) bool { return o == w })
		m.dropIfIdleLocked(tenantID, t, m.now())
		m.mu.Unlock()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("%w: %w", ErrConcurrency, ctx.Err())
	}
	return nil, ctx.Err()
}

// applyLocked brings the tenant's state to lim.
func (m *Manager) applyLocked(t *tenant, lim Limits, now time.Time) {
	if r := max(0, lim.RateRPS); r != t.rateRPS {
		switch {
		case r == 0:
			t.bucket = nil
		case t.bucket == nil:
			// A new limiter starts with a full bucket.
			t.bucket = rate.NewLimiter(rate.Limit(r), r)
		default:
			t.bucket.SetLimitAt(now, rate.Limit(r))
			t.bucket.SetBurstAt(now, r)
		}
		t.rateRPS = r
	}
	t.limit = max(1, lim.Concurrency)
	admitWaitersLocked(t)
}

// admitWaitersLocked admits queued calls while the tenant has room.
func admitWaitersLocked(t *tenant) {
	for t.inFlight < t.limit && len(t.waiters) > 0 {
		w := t.waiters[0]
		t.waiters = slices.Delete(t.waiters, 0, 1)
		t.inFlight++
		w.done = true
		close(w.ready)
	}
}

func (m *Manager) releaser(id string, t *tenant) func() {
	var once sync.Once
	return func() { once.Do(func() { m.release(id, t) }) }
}

func (m *Manager) release(id string, t *tenant) {
	now := m.now()
	m.mu.Lock()
	t.inFlight--
	admitWaitersLocked(t)
	m.dropIfIdleLocked(id, t, now)
	m.mu.Unlock()
}

func (m *Manager) tenant(id string) *tenant {
	t, ok := m.tenants[id]
	if !ok {
		t = &tenant{}
		m.tenants[id] = t
	}
	return t
}

// dropIfIdleLocked forgets a tenant with nothing in flight, nobody queued
// and a full bucket: a new state is the same as the forgotten one, so this
// never resets a quota. A tenant whose last call was rejected keeps its
// state; the map is bounded by the tenants with bindings.
func (m *Manager) dropIfIdleLocked(id string, t *tenant, now time.Time) {
	if t.inFlight > 0 || len(t.waiters) > 0 {
		return
	}
	if t.bucket != nil && t.bucket.TokensAt(now) < float64(t.bucket.Burst()) {
		return
	}
	delete(m.tenants, id)
}

func (m *Manager) tenantCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tenants)
}

// Waiting reports how many calls of tenantID wait in its queue.
func (m *Manager) Waiting(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tenants[id]; ok {
		return len(t.waiters)
	}
	return 0
}
