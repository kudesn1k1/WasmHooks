package pool

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
)

type fakeInst struct {
	id      int
	closed  atomic.Bool
	holders atomic.Int32
}

func (f *fakeInst) Call(context.Context, string, []byte) (sandbox.CallResult, error) {
	return sandbox.CallResult{}, nil
}

func (f *fakeInst) Close(context.Context) error { f.closed.Store(true); return nil }

type factory struct {
	n     atomic.Int32
	delay time.Duration
	fail  error
}

func (f *factory) make(ctx context.Context) (sandbox.Instance, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.fail != nil {
		return nil, f.fail
	}
	return &fakeInst{id: int(f.n.Add(1))}, nil
}

var (
	k1 = Key{TenantID: "a", Hook: "h", HookDefVersion: 1, ModuleHash: "sha256:x", ConfigVersion: 1}
	k2 = Key{TenantID: "b", Hook: "h", HookDefVersion: 1, ModuleHash: "sha256:x", ConfigVersion: 1}
	k3 = Key{TenantID: "c", Hook: "h", HookDefVersion: 1, ModuleHash: "sha256:x", ConfigVersion: 1}
)

func newMgr(t *testing.T, o Options) *Manager {
	t.Helper()
	if o.AcquireTimeout == 0 {
		o.AcquireTimeout = 20 * time.Millisecond
	}
	m := NewManager(o)
	t.Cleanup(func() { m.Close(context.Background()) })
	return m
}

func acquire(t *testing.T, m *Manager, k Key, limit int, f *factory) *Lease {
	t.Helper()
	l, err := m.Acquire(context.Background(), k, limit, f.make)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	return l
}

func TestReuseWarmInstance(t *testing.T) {
	m, f := newMgr(t, Options{}), &factory{}
	l := acquire(t, m, k1, 2, f)
	if !l.Cold() {
		t.Fatal("first lease must be cold")
	}
	first := l.Instance()
	l.Release(true)

	l2 := acquire(t, m, k1, 2, f)
	if l2.Instance() != first || l2.Cold() {
		t.Fatal("want the warm instance back")
	}
	if f.n.Load() != 1 {
		t.Fatalf("factory called %d times", f.n.Load())
	}
}

func TestUnhealthyIsClosed(t *testing.T) {
	m, f := newMgr(t, Options{}), &factory{}
	l := acquire(t, m, k1, 2, f)
	inst := l.Instance().(*fakeInst)
	l.Release(false)
	if !inst.closed.Load() {
		t.Fatal("unhealthy instance must be closed")
	}
	l2 := acquire(t, m, k1, 2, f)
	if l2.Instance() == inst {
		t.Fatal("closed instance was reused")
	}
	if got := m.Stats().Instances; got != 1 {
		t.Fatalf("instances = %d, want 1", got)
	}
}

func TestMaxUsesRecycles(t *testing.T) {
	m, f := newMgr(t, Options{MaxUses: 2}), &factory{}
	l := acquire(t, m, k1, 1, f)
	inst := l.Instance().(*fakeInst)
	l.Release(true)
	acquire(t, m, k1, 1, f).Release(true)
	if !inst.closed.Load() {
		t.Fatal("instance must be closed after MaxUses")
	}
	if l3 := acquire(t, m, k1, 1, f); l3.Instance() == inst {
		t.Fatal("recycled instance was reused")
	}
}

func TestPerKeyLimitSaturates(t *testing.T) {
	m, f := newMgr(t, Options{AcquireTimeout: 30 * time.Millisecond}), &factory{}
	acquire(t, m, k1, 2, f)
	acquire(t, m, k1, 2, f)
	start := time.Now()
	_, err := m.Acquire(context.Background(), k1, 2, f.make)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrSaturated) {
		t.Fatalf("want ErrSaturated, got %v", err)
	}
	if elapsed < 30*time.Millisecond || elapsed > 80*time.Millisecond {
		t.Fatalf("waited %v, want ~30ms", elapsed)
	}
	if f.n.Load() != 2 {
		t.Fatalf("factory called %d times", f.n.Load())
	}
}

func TestWaiterGetsReleasedInstance(t *testing.T) {
	m, f := newMgr(t, Options{AcquireTimeout: time.Second}), &factory{}
	l := acquire(t, m, k1, 1, f)
	held := l.Instance()
	got := make(chan sandbox.Instance, 1)
	go func() {
		l2, err := m.Acquire(context.Background(), k1, 1, f.make)
		if err != nil {
			got <- nil
			return
		}
		got <- l2.Instance()
	}()
	time.Sleep(5 * time.Millisecond)
	l.Release(true)
	select {
	case inst := <-got:
		if inst != held {
			t.Fatal("waiter must receive the released instance")
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not woken")
	}
}

func TestAcquireRespectsContextDeadline(t *testing.T) {
	m, f := newMgr(t, Options{AcquireTimeout: time.Second}), &factory{}
	acquire(t, m, k1, 1, f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := m.Acquire(ctx, k1, 1, f.make)
	if !errors.Is(err, ErrSaturated) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want ErrSaturated wrapping the context error, got %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("ignored ctx deadline: %v", time.Since(start))
	}
}

func TestKeysAreIsolated(t *testing.T) {
	m, f := newMgr(t, Options{}), &factory{}
	l := acquire(t, m, k1, 1, f)
	inst := l.Instance()
	l.Release(true)
	if l2 := acquire(t, m, k2, 1, f); l2.Instance() == inst {
		t.Fatal("instance of k1 was handed to k2")
	}
}

func TestGlobalMaxEvictsIdleOfOtherKey(t *testing.T) {
	m, f := newMgr(t, Options{GlobalMax: 1}), &factory{}
	l := acquire(t, m, k1, 1, f)
	idle := l.Instance().(*fakeInst)
	l.Release(true)

	acquire(t, m, k2, 1, f)
	if !idle.closed.Load() {
		t.Fatal("idle instance of another key must be evicted")
	}
	if got := m.Stats().Instances; got != 1 {
		t.Fatalf("instances = %d, want 1", got)
	}
}

func TestGlobalMaxWaitsWhenNoIdle(t *testing.T) {
	t.Run("saturates", func(t *testing.T) {
		m, f := newMgr(t, Options{GlobalMax: 1}), &factory{}
		acquire(t, m, k1, 1, f)
		if _, err := m.Acquire(context.Background(), k2, 1, f.make); !errors.Is(err, ErrSaturated) {
			t.Fatalf("want ErrSaturated, got %v", err)
		}
	})
	t.Run("proceeds after release", func(t *testing.T) {
		m, f := newMgr(t, Options{GlobalMax: 1, AcquireTimeout: time.Second}), &factory{}
		l := acquire(t, m, k1, 1, f)
		go func() {
			time.Sleep(5 * time.Millisecond)
			l.Release(true)
		}()
		l2, err := m.Acquire(context.Background(), k2, 1, f.make)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if l2.Instance() == l.Instance() {
			t.Fatal("k2 got k1's instance")
		}
	})
}

func TestFactoryErrorFreesSlot(t *testing.T) {
	m := newMgr(t, Options{})
	boom := errors.New("boom")
	if _, err := m.Acquire(context.Background(), k1, 1, (&factory{fail: boom}).make); !errors.Is(err, boom) {
		t.Fatalf("want factory error, got %v", err)
	}
	if got := m.Stats().Instances; got != 0 {
		t.Fatalf("instances = %d, want 0", got)
	}
	acquire(t, m, k1, 1, &factory{})
}

func TestReapIdle(t *testing.T) {
	now := time.Unix(1000, 0)
	m, f := newMgr(t, Options{IdleTTL: time.Minute, Now: func() time.Time { return now }}), &factory{}
	l := acquire(t, m, k1, 1, f)
	inst := l.Instance().(*fakeInst)
	l.Release(true)

	if n := m.ReapIdle(now.Add(30 * time.Second)); n != 0 {
		t.Fatalf("reaped %d before TTL", n)
	}
	if n := m.ReapIdle(now.Add(time.Minute + time.Second)); n != 1 {
		t.Fatalf("reaped %d, want 1", n)
	}
	if !inst.closed.Load() {
		t.Fatal("reaped instance must be closed")
	}
	if s := m.Stats(); s.Pools != 0 || s.Instances != 0 {
		t.Fatalf("stats after reap = %+v", s)
	}
}

func TestDoubleReleaseIsNoop(t *testing.T) {
	m, f := newMgr(t, Options{}), &factory{}
	l := acquire(t, m, k1, 2, f)
	l.Release(true)
	l.Release(true)
	l.Release(false)
	if s := m.Stats(); s.Idle != 1 || s.Instances != 1 {
		t.Fatalf("stats = %+v, want 1 idle instance", s)
	}
}

func TestCloseClosesAll(t *testing.T) {
	m := NewManager(Options{AcquireTimeout: 20 * time.Millisecond})
	f := &factory{}
	idle := acquire(t, m, k1, 3, f)
	held := acquire(t, m, k1, 3, f)
	idleInst, heldInst := idle.Instance().(*fakeInst), held.Instance().(*fakeInst)
	idle.Release(true)

	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !idleInst.closed.Load() {
		t.Fatal("idle instance must be closed by Close")
	}
	if heldInst.closed.Load() {
		t.Fatal("leased instance must stay open until released")
	}
	held.Release(true)
	if !heldInst.closed.Load() {
		t.Fatal("instance released after Close must be closed")
	}
	if _, err := m.Acquire(context.Background(), k1, 1, f.make); !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed, got %v", err)
	}
}

func TestConcurrentStress(t *testing.T) {
	m, f := newMgr(t, Options{GlobalMax: 8, MaxUses: 50, AcquireTimeout: 50 * time.Millisecond}), &factory{}
	keys := []Key{k1, k2, k3}
	var wg sync.WaitGroup
	var violations atomic.Int32
	for range 50 {
		wg.Go(func() {
			for range 200 {
				k := keys[rand.IntN(len(keys))]
				l, err := m.Acquire(context.Background(), k, 4, f.make)
				if errors.Is(err, ErrSaturated) {
					continue
				}
				if err != nil {
					violations.Add(1)
					return
				}
				inst := l.Instance().(*fakeInst)
				if inst.holders.Add(1) != 1 || inst.closed.Load() {
					violations.Add(1)
				}
				if s := m.Stats(); s.Instances > 8 {
					violations.Add(1)
				}
				inst.holders.Add(-1)
				l.Release(rand.IntN(10) != 0)
			}
		})
	}
	wg.Wait()
	if v := violations.Load(); v != 0 {
		t.Fatalf("%d invariant violations", v)
	}
	if s := m.Stats(); s.Instances > 8 || s.Idle != s.Instances {
		t.Fatalf("final stats = %+v", s)
	}
}

func TestFactoryRunsOutsideLock(t *testing.T) {
	m := newMgr(t, Options{AcquireTimeout: time.Second})
	warm := &factory{}
	acquire(t, m, k2, 1, warm).Release(true)

	slow := &factory{delay: 100 * time.Millisecond}
	go m.Acquire(context.Background(), k1, 1, slow.make)
	time.Sleep(5 * time.Millisecond)

	start := time.Now()
	acquire(t, m, k2, 1, warm)
	if d := time.Since(start); d > 20*time.Millisecond {
		t.Fatalf("warm acquire blocked by slow factory: %v", d)
	}
}
