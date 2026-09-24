// Package pool keeps warm sandbox instances per (tenant, hook, module, config)
// key. Instances are never shared across keys: guest memory persists between
// calls, so sharing would leak one tenant's state into another's calls.
//
// A process-wide cap bounds the total number of instances, which bounds the
// executor's memory. When the cap is reached, the oldest idle instance of any
// key is evicted to make room; if none is idle, Acquire waits briefly and then
// fails with ErrSaturated.
package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
)

var (
	// ErrSaturated: no instance became available within the acquire timeout.
	ErrSaturated = errors.New("pool: saturated")
	// ErrClosed: the manager is closed.
	ErrClosed = errors.New("pool: closed")
)

// Key identifies a pool. Any change of script version, hook definition or
// tenant config yields a new key, so stale pools are never invalidated
// explicitly: they idle out.
type Key struct {
	TenantID       string
	Hook           string
	HookDefVersion int64
	ModuleHash     string
	ConfigVersion  int64
}

// Factory creates a new instance for a key. It runs without holding locks.
type Factory func(ctx context.Context) (sandbox.Instance, error)

// Options tune the manager. Zero values take the defaults.
type Options struct {
	GlobalMax      int           // max live instances per process; default 256
	AcquireTimeout time.Duration // max wait for a free instance; default 10ms
	MaxUses        int           // calls before an instance is recycled; default 1000
	IdleTTL        time.Duration // idle instances older than this are closed; default 60s
	ReapInterval   time.Duration // background reaper period; 0 disables it
	Now            func() time.Time
}

func (o *Options) setDefaults() {
	if o.GlobalMax <= 0 {
		o.GlobalMax = 256
	}
	if o.AcquireTimeout <= 0 {
		o.AcquireTimeout = 10 * time.Millisecond
	}
	if o.MaxUses <= 0 {
		o.MaxUses = 1000
	}
	if o.IdleTTL <= 0 {
		o.IdleTTL = 60 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Stats is a point-in-time view of the manager.
type Stats struct {
	Instances int // live instances: idle, leased and being created
	Idle      int
	Pools     int
}

// Manager owns all pools of a process. It is safe for concurrent use.
type Manager struct {
	opts Options

	mu      sync.Mutex
	pools   map[Key]*keyPool
	total   int
	changed chan struct{} // closed and replaced whenever capacity frees up
	closed  bool

	stop     chan struct{}
	reaperWG sync.WaitGroup
}

type keyPool struct {
	idle   []*entry // LIFO: the most recently released instance is the warmest
	active int      // leased plus being created
}

type entry struct {
	inst     sandbox.Instance
	uses     int
	lastUsed time.Time
}

// NewManager creates a manager and starts the reaper if ReapInterval > 0.
func NewManager(opts Options) *Manager {
	opts.setDefaults()
	m := &Manager{
		opts:    opts,
		pools:   make(map[Key]*keyPool),
		changed: make(chan struct{}),
		stop:    make(chan struct{}),
	}
	if opts.ReapInterval > 0 {
		m.reaperWG.Add(1)
		go m.reap()
	}
	return m
}

// Acquire leases an instance for key. limit caps the key's live instances.
// The wait for capacity is bounded by AcquireTimeout and by ctx; either ends
// with ErrSaturated (wrapping ctx.Err() in the second case). Factory errors
// are returned as is.
func (m *Manager) Acquire(ctx context.Context, key Key, limit int, factory Factory) (*Lease, error) {
	if limit < 1 {
		limit = 1
	}
	timer := time.NewTimer(m.opts.AcquireTimeout)
	defer timer.Stop()

	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, ErrClosed
		}
		p := m.pool(key)

		if n := len(p.idle); n > 0 {
			e := p.idle[n-1]
			p.idle = p.idle[:n-1]
			p.active++
			m.mu.Unlock()
			return &Lease{m: m, key: key, e: e}, nil
		}

		if p.active+len(p.idle) < limit {
			if m.total < m.opts.GlobalMax {
				m.total++
				p.active++
				m.mu.Unlock()
				return m.create(ctx, key, factory)
			}
			if victim := m.evictOldestIdleLocked(); victim != nil {
				m.mu.Unlock()
				victim.Close(context.Background())
				continue
			}
		}

		wake := m.changed
		m.mu.Unlock()

		select {
		case <-wake:
		case <-timer.C:
			return nil, m.saturated(key, nil)
		case <-ctx.Done():
			return nil, m.saturated(key, ctx.Err())
		}
	}
}

// saturated drops the key's pool if the failed wait left it empty. When the
// caller's context ended the wait, the error wraps both ErrSaturated and the
// context error, so callers can tell a busy tenant from an exhausted budget.
func (m *Manager) saturated(key Key, ctxErr error) error {
	m.mu.Lock()
	m.dropIfEmptyLocked(key)
	m.mu.Unlock()
	if ctxErr != nil {
		return fmt.Errorf("%w: %w", ErrSaturated, ctxErr)
	}
	return ErrSaturated
}

// create runs the factory for a slot already reserved under the lock.
func (m *Manager) create(ctx context.Context, key Key, factory Factory) (*Lease, error) {
	inst, err := factory(ctx)
	if err != nil {
		m.mu.Lock()
		m.total--
		m.pool(key).active--
		m.dropIfEmptyLocked(key)
		m.notifyLocked()
		m.mu.Unlock()
		return nil, err
	}
	return &Lease{m: m, key: key, e: &entry{inst: inst}, cold: true}, nil
}

// release returns or retires a leased instance.
func (m *Manager) release(key Key, e *entry, healthy bool) {
	e.uses++
	m.mu.Lock()
	p := m.pool(key)
	p.active--
	retire := !healthy || e.uses >= m.opts.MaxUses || m.closed
	if retire {
		m.total--
		m.dropIfEmptyLocked(key)
	} else {
		e.lastUsed = m.opts.Now()
		p.idle = append(p.idle, e)
	}
	m.notifyLocked()
	m.mu.Unlock()

	if retire {
		e.inst.Close(context.Background())
	}
}

// ReapIdle closes instances idle for longer than IdleTTL as of now and drops
// empty pools. It returns the number of instances closed.
func (m *Manager) ReapIdle(now time.Time) int {
	var victims []sandbox.Instance
	m.mu.Lock()
	for key, p := range m.pools {
		kept := p.idle[:0]
		for _, e := range p.idle {
			if now.Sub(e.lastUsed) > m.opts.IdleTTL {
				victims = append(victims, e.inst)
				m.total--
			} else {
				kept = append(kept, e)
			}
		}
		clear(p.idle[len(kept):])
		p.idle = kept
		m.dropIfEmptyLocked(key)
	}
	if len(victims) > 0 {
		m.notifyLocked()
	}
	m.mu.Unlock()

	for _, inst := range victims {
		inst.Close(context.Background())
	}
	return len(victims)
}

// Stats reports current counts.
func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Stats{Instances: m.total, Pools: len(m.pools)}
	for _, p := range m.pools {
		s.Idle += len(p.idle)
	}
	return s
}

// Close stops the reaper and closes idle instances. Leased instances are
// closed when released. Acquire fails with ErrClosed afterwards.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	var victims []sandbox.Instance
	for key, p := range m.pools {
		for _, e := range p.idle {
			victims = append(victims, e.inst)
			m.total--
		}
		p.idle = nil
		m.dropIfEmptyLocked(key)
	}
	m.notifyLocked()
	m.mu.Unlock()

	close(m.stop)
	m.reaperWG.Wait()

	var errs []error
	for _, inst := range victims {
		errs = append(errs, inst.Close(ctx))
	}
	return errors.Join(errs...)
}

func (m *Manager) reap() {
	defer m.reaperWG.Done()
	t := time.NewTicker(m.opts.ReapInterval)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.ReapIdle(m.opts.Now())
		}
	}
}

func (m *Manager) pool(key Key) *keyPool {
	p, ok := m.pools[key]
	if !ok {
		p = &keyPool{}
		m.pools[key] = p
	}
	return p
}

func (m *Manager) dropIfEmptyLocked(key Key) {
	if p, ok := m.pools[key]; ok && p.active == 0 && len(p.idle) == 0 {
		delete(m.pools, key)
	}
}

// evictOldestIdleLocked removes the longest-idle instance of any key and
// returns it for closing outside the lock, or nil if nothing is idle.
func (m *Manager) evictOldestIdleLocked() sandbox.Instance {
	var (
		victimKey Key
		victimIdx = -1
		oldest    time.Time
	)
	for key, p := range m.pools {
		for i, e := range p.idle {
			if victimIdx < 0 || e.lastUsed.Before(oldest) {
				victimKey, victimIdx, oldest = key, i, e.lastUsed
			}
		}
	}
	if victimIdx < 0 {
		return nil
	}
	p := m.pools[victimKey]
	e := p.idle[victimIdx]
	p.idle = append(p.idle[:victimIdx], p.idle[victimIdx+1:]...)
	m.total--
	m.dropIfEmptyLocked(victimKey)
	return e.inst
}

func (m *Manager) notifyLocked() {
	close(m.changed)
	m.changed = make(chan struct{})
}

// Lease is exclusive access to one instance until Release.
type Lease struct {
	m    *Manager
	key  Key
	e    *entry
	cold bool
	once sync.Once
}

// Instance returns the leased instance.
func (l *Lease) Instance() sandbox.Instance { return l.e.inst }

// Cold reports whether the instance was created for this lease.
func (l *Lease) Cold() bool { return l.cold }

// Release returns the instance to its pool if healthy, otherwise closes it.
// Only the first call has an effect.
func (l *Lease) Release(healthy bool) {
	l.once.Do(func() { l.m.release(l.key, l.e, healthy) })
}
