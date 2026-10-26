package quota

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newManager() (*Manager, *clock) {
	c := &clock{now: time.Unix(1_000_000, 0)}
	return New(c.Now), c
}

type result struct {
	release func()
	err     error
}

func admitAsync(m *Manager, ctx context.Context, tenant string, lim Limits) <-chan result {
	ch := make(chan result, 1)
	go func() {
		release, err := m.Admit(ctx, tenant, lim)
		ch <- result{release, err}
	}()
	return ch
}

func mustAdmit(t *testing.T, m *Manager, tenant string, lim Limits) func() {
	t.Helper()
	release, err := m.Admit(context.Background(), tenant, lim)
	if err != nil {
		t.Fatalf("Admit(%s, %+v): %v", tenant, lim, err)
	}
	return release
}

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

func received(t *testing.T, ch <-chan result) result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(time.Second):
		t.Fatal("Admit did not return")
		return result{}
	}
}

func pending(t *testing.T, ch <-chan result) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("Admit returned early: err=%v", r.err)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRateLimit(t *testing.T) {
	m, c := newManager()
	lim := Limits{RateRPS: 5, Concurrency: 100}
	for i := range 5 {
		if _, err := m.Admit(context.Background(), "a", lim); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if _, err := m.Admit(context.Background(), "a", lim); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("6th call: err = %v, want ErrRateLimited", err)
	}
	// Another tenant has its own bucket.
	mustAdmit(t, m, "b", lim)

	c.Advance(200 * time.Millisecond) // one token at 5/s
	mustAdmit(t, m, "a", lim)
	if _, err := m.Admit(context.Background(), "a", lim); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("after refill: err = %v, want ErrRateLimited", err)
	}
}

// TestRateSurvivesIdle: a tenant with nothing in flight keeps its bucket, so
// sequential calls cannot reset the rate limit.
func TestRateSurvivesIdle(t *testing.T) {
	m, _ := newManager()
	lim := Limits{RateRPS: 2, Concurrency: 1}
	mustAdmit(t, m, "a", lim)()
	mustAdmit(t, m, "a", lim)()
	if _, err := m.Admit(context.Background(), "a", lim); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestRateUnlimited(t *testing.T) {
	m, _ := newManager()
	for range 1000 {
		mustAdmit(t, m, "a", Limits{RateRPS: 0, Concurrency: 1})()
	}
	if n := m.tenantCount(); n != 0 {
		t.Fatalf("%d tenants left, want 0", n)
	}
}

func TestRateTransitions(t *testing.T) {
	m, c := newManager()
	unlimited := Limits{RateRPS: 0, Concurrency: 10}
	five := Limits{RateRPS: 5, Concurrency: 10}

	for range 20 {
		mustAdmit(t, m, "a", unlimited)()
	}
	// 0 -> 5: a fresh, full bucket of 5.
	for range 5 {
		mustAdmit(t, m, "a", five)()
	}
	if _, err := m.Admit(context.Background(), "a", five); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("0->5: err = %v, want ErrRateLimited", err)
	}
	// 5 -> 0: no limit at once, although the bucket was empty.
	for range 20 {
		mustAdmit(t, m, "a", unlimited)()
	}
	// 0 -> 5 again: full bucket again.
	for range 5 {
		mustAdmit(t, m, "a", five)()
	}
	// 5 -> 10 on the fly: the new rate refills faster.
	ten := Limits{RateRPS: 10, Concurrency: 10}
	if _, err := m.Admit(context.Background(), "a", ten); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("5->10: err = %v, want ErrRateLimited", err)
	}
	c.Advance(100 * time.Millisecond) // one token at 10/s, half a token at 5/s
	mustAdmit(t, m, "a", ten)()
}

func TestConcurrencyWaits(t *testing.T) {
	m, _ := newManager()
	lim := Limits{Concurrency: 2}
	r1 := mustAdmit(t, m, "a", lim)
	r2 := mustAdmit(t, m, "a", lim)

	third := admitAsync(m, context.Background(), "a", lim)
	pending(t, third)
	// Another tenant is not affected.
	mustAdmit(t, m, "b", lim)()

	r1()
	r := received(t, third)
	if r.err != nil {
		t.Fatal(r.err)
	}
	r.release()
	r2()
	if n := m.tenantCount(); n != 0 {
		t.Fatalf("%d tenants left, want 0", n)
	}
}

func TestConcurrencyQueueFull(t *testing.T) {
	m, _ := newManager()
	lim := Limits{Concurrency: 1} // the queue holds one waiter
	hold := mustAdmit(t, m, "a", lim)
	waiting := admitAsync(m, context.Background(), "a", lim)
	waitFor(t, "a queued", func() bool { return m.Waiting("a") == 1 })

	if _, err := m.Admit(context.Background(), "a", lim); !errors.Is(err, ErrConcurrency) {
		t.Fatalf("err = %v, want ErrConcurrency", err)
	}
	hold()
	r := received(t, waiting)
	if r.err != nil {
		t.Fatal(r.err)
	}
	r.release()
}

func TestConcurrencyDeadline(t *testing.T) {
	m, _ := newManager()
	lim := Limits{Concurrency: 1}
	hold := mustAdmit(t, m, "a", lim)
	defer hold()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := m.Admit(ctx, "a", lim)
	if !errors.Is(err, ErrConcurrency) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want ErrConcurrency wrapping DeadlineExceeded", err)
	}
	if n := m.Waiting("a"); n != 0 {
		t.Fatalf("%d waiters left", n)
	}
}

func TestConcurrencyCancel(t *testing.T) {
	m, _ := newManager()
	lim := Limits{Concurrency: 1}
	hold := mustAdmit(t, m, "a", lim)

	ctx, cancel := context.WithCancel(context.Background())
	ch := admitAsync(m, ctx, "a", lim)
	waitFor(t, "a queued", func() bool { return m.Waiting("a") == 1 })
	cancel()
	r := received(t, ch)
	if !errors.Is(r.err, context.Canceled) || errors.Is(r.err, ErrConcurrency) {
		t.Fatalf("err = %v, want context.Canceled only", r.err)
	}
	hold()
	if n := m.tenantCount(); n != 0 {
		t.Fatalf("%d tenants left, want 0", n)
	}
}

// TestConcurrencyLowered: the limit drops from 8 to 2 with 8 calls in
// flight. New calls wait until fewer than 2 are in flight.
func TestConcurrencyLowered(t *testing.T) {
	m, _ := newManager()
	var held []func()
	for range 8 {
		held = append(held, mustAdmit(t, m, "a", Limits{Concurrency: 8}))
	}
	low := Limits{Concurrency: 2}
	ch := admitAsync(m, context.Background(), "a", low)
	waitFor(t, "a queued", func() bool { return m.Waiting("a") == 1 })

	for _, release := range held[:6] {
		release()
	}
	pending(t, ch) // 2 in flight, limit 2
	held[6]()
	r := received(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	r.release()
	held[7]()
	if n := m.tenantCount(); n != 0 {
		t.Fatalf("%d tenants left, want 0", n)
	}
}

// TestConcurrencyRaised: a raised limit, seen on the next call, wakes the
// waiters it now has room for.
func TestConcurrencyRaised(t *testing.T) {
	m, _ := newManager()
	hold := mustAdmit(t, m, "a", Limits{Concurrency: 1})
	waiting := admitAsync(m, context.Background(), "a", Limits{Concurrency: 1})
	waitFor(t, "a queued", func() bool { return m.Waiting("a") == 1 })

	next := mustAdmit(t, m, "a", Limits{Concurrency: 3})
	r := received(t, waiting)
	if r.err != nil {
		t.Fatal(r.err)
	}
	r.release()
	next()
	hold()
	if n := m.tenantCount(); n != 0 {
		t.Fatalf("%d tenants left, want 0", n)
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	m, _ := newManager()
	lim := Limits{Concurrency: 1}
	release := mustAdmit(t, m, "a", lim)
	release()
	release()
	other := mustAdmit(t, m, "a", lim)
	ch := admitAsync(m, context.Background(), "a", lim)
	pending(t, ch) // a second release must not have freed a phantom slot
	other()
	received(t, ch).release()
}

// TestCancelRacesWake cancels a waiter at the moment a release wakes it.
// Whichever wins, nothing stays in flight.
func TestCancelRacesWake(t *testing.T) {
	m, _ := newManager()
	lim := Limits{Concurrency: 1}
	for range 500 {
		hold := mustAdmit(t, m, "a", lim)
		ctx, cancel := context.WithCancel(context.Background())
		ch := admitAsync(m, ctx, "a", lim)
		waitFor(t, "a queued", func() bool { return m.Waiting("a") == 1 })

		var wg sync.WaitGroup
		wg.Go(hold)
		wg.Go(cancel)
		wg.Wait()

		if r := received(t, ch); r.err == nil {
			r.release()
		} else if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("err = %v", r.err)
		}
		if n := m.tenantCount(); n != 0 {
			t.Fatalf("%d tenants left, want 0", n)
		}
	}
}

// TestStaleLimitsIgnored: a call that read the previous snapshot just before
// a change does not roll the change back (review 1, M3).
func TestStaleLimitsIgnored(t *testing.T) {
	m, _ := newManager()
	for range 5 {
		mustAdmit(t, m, "a", Limits{RateRPS: 5, Concurrency: 10, Version: 2})()
	}
	// Version 1 had no rate limit; it must not drop the bucket.
	if _, err := m.Admit(context.Background(), "a", Limits{RateRPS: 0, Concurrency: 10, Version: 1}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("stale limits applied: err = %v, want ErrRateLimited", err)
	}
	if _, err := m.Admit(context.Background(), "a", Limits{RateRPS: 5, Concurrency: 10, Version: 2}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

// TestDoneContextMeansNoWait: a done ctx is admitted while there is room,
// but never waits for room.
func TestDoneContextMeansNoWait(t *testing.T) {
	m, _ := newManager()
	lim := Limits{Concurrency: 1}
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	release, err := m.Admit(expired, "a", lim)
	if err != nil {
		t.Fatalf("room left: err = %v", err)
	}
	if _, err := m.Admit(expired, "a", lim); !errors.Is(err, ErrConcurrency) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("no room: err = %v, want ErrConcurrency wrapping DeadlineExceeded", err)
	}
	canceled, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := m.Admit(canceled, "a", lim); !errors.Is(err, context.Canceled) || errors.Is(err, ErrConcurrency) {
		t.Fatalf("no room, canceled: err = %v, want context.Canceled only", err)
	}
	release()
	if n := m.tenantCount(); n != 0 {
		t.Fatalf("%d tenants left, want 0", n)
	}
}
