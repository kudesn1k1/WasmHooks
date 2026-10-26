package slots

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// acquireAsync starts Acquire in a goroutine and returns a channel with its
// outcome.
type result struct {
	release func()
	err     error
}

func acquireAsync(l *Limiter, ctx context.Context, tenant string) <-chan result {
	ch := make(chan result, 1)
	go func() {
		release, err := l.Acquire(ctx, tenant)
		ch <- result{release, err}
	}()
	return ch
}

// waitFor polls cond until it holds or a second passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func mustAcquire(t *testing.T, l *Limiter, tenant string) func() {
	t.Helper()
	release, err := l.Acquire(context.Background(), tenant)
	if err != nil {
		t.Fatalf("Acquire(%s): %v", tenant, err)
	}
	return release
}

func received(t *testing.T, ch <-chan result) result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(time.Second):
		t.Fatal("Acquire did not return")
		return result{}
	}
}

func pending(t *testing.T, ch <-chan result) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("Acquire returned early: err=%v", r.err)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestDefaults(t *testing.T) {
	l := New(Options{})
	want := max(1, runtime.GOMAXPROCS(0)-1)
	if l.Capacity() != want {
		t.Fatalf("Capacity = %d, want %d", l.Capacity(), want)
	}
	if l.TenantShare() != max(1, want/2) {
		t.Fatalf("TenantShare = %d, want %d", l.TenantShare(), max(1, want/2))
	}
	if l.Queue() != 4*want {
		t.Fatalf("Queue = %d, want %d", l.Queue(), 4*want)
	}

	l = New(Options{Capacity: 3, TenantShare: 10})
	if l.TenantShare() != 3 {
		t.Fatalf("TenantShare above capacity = %d, want 3", l.TenantShare())
	}
}

func TestAcquireFreeSlot(t *testing.T) {
	l := New(Options{Capacity: 2})
	release := mustAcquire(t, l, "a")
	if s := l.Stats(); s.Running != 1 || s.Waiting != 0 {
		t.Fatalf("Stats = %+v", s)
	}
	release()
	if s := l.Stats(); s != (Stats{}) {
		t.Fatalf("Stats after release = %+v", s)
	}
}

func TestTenantShare(t *testing.T) {
	l := New(Options{Capacity: 4, TenantShare: 2})
	a1 := mustAcquire(t, l, "a")
	mustAcquire(t, l, "a")

	third := acquireAsync(l, context.Background(), "a")
	pending(t, third)

	// Another tenant runs at once although tenant a is waiting.
	b := mustAcquire(t, l, "b")
	if s := l.Stats(); s.Running != 3 || s.Waiting != 1 {
		t.Fatalf("Stats = %+v", s)
	}

	a1()
	r := received(t, third)
	if r.err != nil {
		t.Fatal(r.err)
	}
	r.release()
	b()
}

func TestQueueBounds(t *testing.T) {
	l := New(Options{Capacity: 1, Queue: 2}) // tenant queue share 1
	hold := mustAcquire(t, l, "a")

	a2 := acquireAsync(l, context.Background(), "a")
	waitFor(t, "a2 queued", func() bool { return l.Stats().Waiting == 1 })

	// Tenant a has used its share of the queue.
	if _, err := l.Acquire(context.Background(), "a"); !errors.Is(err, ErrOverloaded) {
		t.Fatalf("a3: err = %v, want ErrOverloaded", err)
	}
	// Tenant b still finds room in the queue.
	b1 := acquireAsync(l, context.Background(), "b")
	waitFor(t, "b1 queued", func() bool { return l.Stats().Waiting == 2 })
	// The queue is full for everyone now.
	if _, err := l.Acquire(context.Background(), "c"); !errors.Is(err, ErrOverloaded) {
		t.Fatalf("c1: err = %v, want ErrOverloaded", err)
	}

	hold()
	// a and b both hold 0 slots; a was served last, so b goes first.
	first := received(t, b1)
	if first.err != nil {
		t.Fatal(first.err)
	}
	pending(t, a2)
	first.release()
	second := received(t, a2)
	if second.err != nil {
		t.Fatal(second.err)
	}
	second.release()
	if s := l.Stats(); s != (Stats{}) {
		t.Fatalf("Stats = %+v", s)
	}
}

func TestReleaseGoesToLeastOccupiedTenant(t *testing.T) {
	l := New(Options{Capacity: 2, TenantShare: 2, Queue: 8})
	a1 := mustAcquire(t, l, "a")
	a2 := mustAcquire(t, l, "a")

	a3 := acquireAsync(l, context.Background(), "a")
	waitFor(t, "a3 queued", func() bool { return l.Stats().Waiting == 1 })
	b1 := acquireAsync(l, context.Background(), "b")
	waitFor(t, "b1 queued", func() bool { return l.Stats().Waiting == 2 })

	// a still holds one slot, b none: b gets the freed slot although a3
	// has waited longer.
	a1()
	rb := received(t, b1)
	if rb.err != nil {
		t.Fatal(rb.err)
	}
	pending(t, a3)

	a2()
	ra := received(t, a3)
	if ra.err != nil {
		t.Fatal(ra.err)
	}
	ra.release()
	rb.release()
	if s := l.Stats(); s != (Stats{}) {
		t.Fatalf("Stats = %+v", s)
	}
}

func TestWaiterAtShareIsSkipped(t *testing.T) {
	l := New(Options{Capacity: 2, TenantShare: 1, Queue: 8})
	a1 := mustAcquire(t, l, "a")
	b1 := mustAcquire(t, l, "b")

	a2 := acquireAsync(l, context.Background(), "a")
	waitFor(t, "a2 queued", func() bool { return l.Stats().Waiting == 1 })
	c1 := acquireAsync(l, context.Background(), "c")
	waitFor(t, "c1 queued", func() bool { return l.Stats().Waiting == 2 })

	// b's slot frees up; a is at its share, so c gets it.
	b1()
	rc := received(t, c1)
	if rc.err != nil {
		t.Fatal(rc.err)
	}
	pending(t, a2)

	a1()
	ra := received(t, a2)
	if ra.err != nil {
		t.Fatal(ra.err)
	}
	ra.release()
	rc.release()
}

func TestCancelWhileWaiting(t *testing.T) {
	l := New(Options{Capacity: 1})
	hold := mustAcquire(t, l, "a")

	ctx, cancel := context.WithCancel(context.Background())
	ch := acquireAsync(l, ctx, "b")
	waitFor(t, "b queued", func() bool { return l.Stats().Waiting == 1 })
	cancel()
	if r := received(t, ch); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", r.err)
	}
	if s := l.Stats(); s.Running != 1 || s.Waiting != 0 {
		t.Fatalf("Stats = %+v", s)
	}
	hold()
	if s := l.Stats(); s != (Stats{}) {
		t.Fatalf("Stats = %+v", s)
	}
}

func TestDeadlineWhileWaiting(t *testing.T) {
	l := New(Options{Capacity: 1})
	hold := mustAcquire(t, l, "a")
	defer hold()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(ctx, "b"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

// TestDoneContextMeansNoWait: a done ctx still takes a free slot, but never
// waits for one.
func TestDoneContextMeansNoWait(t *testing.T) {
	l := New(Options{Capacity: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := l.Acquire(ctx, "a")
	if err != nil {
		t.Fatalf("free slot: err = %v", err)
	}
	if _, err := l.Acquire(ctx, "b"); !errors.Is(err, context.Canceled) {
		t.Fatalf("busy slot: err = %v, want context.Canceled", err)
	}
	if s := l.Stats(); s.Running != 1 || s.Waiting != 0 {
		t.Fatalf("Stats = %+v", s)
	}
	release()
	if n := l.tenantCount(); n != 0 {
		t.Fatalf("%d tenants left in the map", n)
	}
}

// TestTieGoesToLeastRecentlyServed: with one slot (share = capacity) a
// flooding tenant's queued calls do not hold a neighbour back (review 1, I2).
func TestTieGoesToLeastRecentlyServed(t *testing.T) {
	l := New(Options{Capacity: 1, Queue: 8})
	b1 := mustAcquire(t, l, "b")
	b2 := acquireAsync(l, context.Background(), "b")
	waitFor(t, "b2 queued", func() bool { return l.Stats().Waiting == 1 })
	b3 := acquireAsync(l, context.Background(), "b")
	waitFor(t, "b3 queued", func() bool { return l.Stats().Waiting == 2 })
	a := acquireAsync(l, context.Background(), "a")
	waitFor(t, "a queued", func() bool { return l.Stats().Waiting == 3 })

	b1()
	ra := received(t, a) // b was served last, a never: a goes first
	if ra.err != nil {
		t.Fatal(ra.err)
	}
	pending(t, b2)
	ra.release()
	rb := received(t, b2)
	if rb.err != nil {
		t.Fatal(rb.err)
	}
	rb.release()
	received(t, b3).release()
}

func TestReleaseIsIdempotent(t *testing.T) {
	l := New(Options{Capacity: 2})
	a := mustAcquire(t, l, "a")
	b := mustAcquire(t, l, "b")
	a()
	a()
	if s := l.Stats(); s.Running != 1 {
		t.Fatalf("Stats = %+v, want one running", s)
	}
	b()
	if s := l.Stats(); s != (Stats{}) {
		t.Fatalf("Stats = %+v", s)
	}
}

// TestCancelRacesGrant cancels a waiter at the moment its slot is granted.
// Whichever wins, the slot is neither lost nor handed out twice.
func TestCancelRacesGrant(t *testing.T) {
	l := New(Options{Capacity: 1})
	for range 500 {
		hold := mustAcquire(t, l, "a")
		ctx, cancel := context.WithCancel(context.Background())
		ch := acquireAsync(l, ctx, "b")
		waitFor(t, "b queued", func() bool { return l.Stats().Waiting == 1 })

		var wg sync.WaitGroup
		wg.Go(hold)
		wg.Go(cancel)
		wg.Wait()

		r := received(t, ch)
		if r.err == nil {
			r.release()
		} else if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("err = %v", r.err)
		}
		if s := l.Stats(); s != (Stats{}) {
			t.Fatalf("Stats = %+v", s)
		}
	}
	if n := l.tenantCount(); n != 0 {
		t.Fatalf("%d tenants left in the map", n)
	}
}

// TestConcurrentUse hammers the limiter from many goroutines and checks the
// bounds hold at every grant.
func TestConcurrentUse(t *testing.T) {
	const capacity, share = 3, 1
	l := New(Options{Capacity: capacity, TenantShare: share, Queue: 64})
	var (
		mu      sync.Mutex
		running int
		per     = map[string]int{}
	)
	var wg sync.WaitGroup
	for i := range 24 {
		tenant := string(rune('a' + i%4))
		wg.Go(func() {
			for range 50 {
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				release, err := l.Acquire(ctx, tenant)
				cancel()
				if err != nil {
					continue
				}
				mu.Lock()
				running++
				per[tenant]++
				if running > capacity || per[tenant] > share {
					mu.Unlock()
					t.Errorf("bounds broken: running %d, %s holds %d", running, tenant, per[tenant])
					release()
					return
				}
				mu.Unlock()
				time.Sleep(100 * time.Microsecond)
				mu.Lock()
				running--
				per[tenant]--
				mu.Unlock()
				release()
			}
		})
	}
	wg.Wait()
	if s := l.Stats(); s != (Stats{}) {
		t.Fatalf("Stats = %+v", s)
	}
	if n := l.tenantCount(); n != 0 {
		t.Fatalf("%d tenants left in the map", n)
	}
}
