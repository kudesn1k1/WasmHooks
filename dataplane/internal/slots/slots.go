// Package slots bounds how many tenant scripts run at once in an executor.
//
// wasm code is not preempted asynchronously: once spinning scripts hold
// every P, the runtime's deadline watcher and other tenants' calls wait for
// the scheduler (spike S7b). A Limiter keeps running calls below GOMAXPROCS
// by default, caps the slots one tenant may hold, and queues the rest in a
// bounded queue that one tenant cannot fill alone. A freed slot goes to the
// waiting tenant that holds the fewest slots and, on a tie, to the one served
// least recently, so a tenant flooding the queue does not delay the others
// (decision D30, spec M2). Isolation needs TenantShare below Capacity: with
// one slot, or a share of every slot, one tenant can hold them all.
package slots

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
)

// ErrOverloaded: the queue, or the tenant's share of it, is full.
var ErrOverloaded = errors.New("slots: overloaded")

// Options size a Limiter. Zero values take the defaults.
type Options struct {
	Capacity    int // running calls; default max(1, GOMAXPROCS-1)
	TenantShare int // running calls of one tenant; default max(1, Capacity/2), at most Capacity
	Queue       int // waiting calls; default 4*Capacity, one tenant may hold max(1, Queue/2)
}

// Stats is a point-in-time view of a Limiter.
type Stats struct {
	Running int
	Waiting int
}

// Limiter is safe for concurrent use.
type Limiter struct {
	capacity, share, queue, queueShare int

	mu      sync.Mutex
	running int
	served  uint64 // grants so far; stamps tenant.served
	tenants map[string]*tenant
	waiters []*waiter // in arrival order
}

type tenant struct {
	running, waiting int
	served           uint64 // when the tenant last got a slot; 0: never since it was idle
}

type waiter struct {
	id      string
	t       *tenant
	granted chan struct{} // closed under mu when the slot is handed over
	done    bool
}

// New creates a Limiter.
func New(opts Options) *Limiter {
	if opts.Capacity <= 0 {
		// One P stays free for the deadline watcher that closes a module
		// whose call ran out of time.
		opts.Capacity = max(1, runtime.GOMAXPROCS(0)-1)
	}
	if opts.TenantShare <= 0 {
		opts.TenantShare = max(1, opts.Capacity/2)
	}
	if opts.Queue <= 0 {
		opts.Queue = 4 * opts.Capacity
	}
	return &Limiter{
		capacity:   opts.Capacity,
		share:      min(opts.TenantShare, opts.Capacity),
		queue:      opts.Queue,
		queueShare: max(1, opts.Queue/2),
		tenants:    make(map[string]*tenant),
	}
}

// Capacity returns how many calls may run at once.
func (l *Limiter) Capacity() int { return l.capacity }

// TenantShare returns how many calls of one tenant may run at once.
func (l *Limiter) TenantShare() int { return l.share }

// Queue returns how many calls may wait for a slot.
func (l *Limiter) Queue() int { return l.queue }

// Acquire takes a slot for tenantID. A slot free to the tenant is taken at
// once, whatever the state of ctx; otherwise the call waits while ctx
// allows, so a ctx that is already done means "no waiting". It fails with
// ErrOverloaded at once when the queue or the tenant's share of the queue is
// full, and with ctx.Err() when ctx ends first. release frees the slot; only
// its first call has an effect.
func (l *Limiter) Acquire(ctx context.Context, tenantID string) (release func(), err error) {
	l.mu.Lock()
	t := l.tenant(tenantID)
	// A freed slot is granted to an eligible waiter at once, so with a slot
	// free no eligible waiter exists and taking it jumps nobody.
	if l.running < l.capacity && t.running < l.share {
		l.grantLocked(t)
		l.mu.Unlock()
		return l.releaser(tenantID, t), nil
	}
	if err := ctx.Err(); err != nil {
		l.dropIfIdleLocked(tenantID, t)
		l.mu.Unlock()
		return nil, err
	}
	if len(l.waiters) >= l.queue || t.waiting >= l.queueShare {
		l.dropIfIdleLocked(tenantID, t)
		l.mu.Unlock()
		return nil, ErrOverloaded
	}
	w := &waiter{id: tenantID, t: t, granted: make(chan struct{})}
	l.waiters = append(l.waiters, w)
	t.waiting++
	l.mu.Unlock()

	select {
	case <-w.granted:
		return l.releaser(tenantID, t), nil
	case <-ctx.Done():
	}
	l.mu.Lock()
	if w.done {
		// The slot was granted as ctx ended: hand it on.
		l.mu.Unlock()
		l.release(tenantID, t)
		return nil, ctx.Err()
	}
	l.waiters = slices.DeleteFunc(l.waiters, func(o *waiter) bool { return o == w })
	t.waiting--
	l.dropIfIdleLocked(tenantID, t)
	l.mu.Unlock()
	return nil, ctx.Err()
}

func (l *Limiter) releaser(id string, t *tenant) func() {
	var once sync.Once
	return func() { once.Do(func() { l.release(id, t) }) }
}

func (l *Limiter) release(id string, t *tenant) {
	l.mu.Lock()
	t.running--
	l.running--
	l.handOverLocked()
	l.dropIfIdleLocked(id, t)
	l.mu.Unlock()
}

// grantLocked gives t a slot.
func (l *Limiter) grantLocked(t *tenant) {
	t.running++
	l.running++
	l.served++
	t.served = l.served
}

// handOverLocked hands free slots to waiting tenants below their share: the
// tenant holding the fewest slots first, then the one served least
// recently, then the oldest waiter. The second rule keeps a flooding tenant
// from winning every tie with its own queued calls when shares are large.
func (l *Limiter) handOverLocked() {
	for l.running < l.capacity {
		best := -1
		for i, w := range l.waiters {
			if w.t.running >= l.share {
				continue
			}
			if best < 0 {
				best = i
				continue
			}
			b := l.waiters[best].t
			if w.t.running < b.running || (w.t.running == b.running && w.t.served < b.served) {
				best = i
			}
		}
		if best < 0 {
			return
		}
		w := l.waiters[best]
		l.waiters = slices.Delete(l.waiters, best, best+1)
		w.t.waiting--
		l.grantLocked(w.t)
		w.done = true
		close(w.granted)
	}
}

// tenant returns the state of a tenant, creating it.
func (l *Limiter) tenant(id string) *tenant {
	t, ok := l.tenants[id]
	if !ok {
		t = &tenant{}
		l.tenants[id] = t
	}
	return t
}

// dropIfIdleLocked forgets a tenant that neither runs nor waits, so the map
// does not grow with every tenant that ever called. Nobody else holds t
// then: a slot holder or a waiter keeps the tenant's counts above zero.
func (l *Limiter) dropIfIdleLocked(id string, t *tenant) {
	if t.running == 0 && t.waiting == 0 {
		delete(l.tenants, id)
	}
}

// Stats reports current counts.
func (l *Limiter) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Stats{Running: l.running, Waiting: len(l.waiters)}
}

func (l *Limiter) tenantCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.tenants)
}
