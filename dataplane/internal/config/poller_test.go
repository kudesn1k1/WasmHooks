package config

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// reply is one scripted answer of the fake control plane.
type reply struct {
	status int
	body   string
}

// fakeControlPlane answers requests from a script, then blocks like a
// long-poll with no changes until the client gives up.
type fakeControlPlane struct {
	mu      sync.Mutex
	script  []reply
	queries []string
	times   []time.Time
}

func (f *fakeControlPlane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.queries = append(f.queries, r.URL.RawQuery)
	f.times = append(f.times, time.Now())
	if len(f.script) == 0 {
		f.mu.Unlock()
		<-r.Context().Done()
		return
	}
	next := f.script[0]
	f.script = f.script[1:]
	f.mu.Unlock()
	w.WriteHeader(next.status)
	fmt.Fprint(w, next.body)
}

func (f *fakeControlPlane) seen() ([]string, []time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...), append([]time.Time(nil), f.times...)
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type pollerRun struct {
	fake    *fakeControlPlane
	store   *Store
	logs    *syncBuffer
	updates chan int64
	cancel  context.CancelFunc
	done    chan error
}

func startPoller(t *testing.T, minBackoff, maxBackoff time.Duration, script ...reply) *pollerRun {
	t.Helper()
	fake := &fakeControlPlane{script: script}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	run := &pollerRun{
		fake:    fake,
		store:   NewStore(),
		logs:    &syncBuffer{},
		updates: make(chan int64, 16),
		done:    make(chan error, 1),
	}
	p := &Poller{
		Source:     &HTTPSource{BaseURL: srv.URL, Token: "t", WaitS: 1},
		Store:      run.store,
		OnUpdate:   func(s *Snapshot) { run.updates <- s.Version },
		Log:        slog.New(slog.NewTextHandler(run.logs, nil)),
		MinBackoff: minBackoff,
		MaxBackoff: maxBackoff,
		Rand:       func() float64 { return 1 },
	}
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	go func() { run.done <- p.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-run.done
	})
	return run
}

func (r *pollerRun) waitUpdate(t *testing.T, want int64) {
	t.Helper()
	select {
	case got := <-r.updates:
		if got != want {
			t.Fatalf("OnUpdate version = %d, want %d", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no OnUpdate for version %d; logs:\n%s", want, r.logs.String())
	}
}

// waitRequests waits until the fake has seen n requests.
func (r *pollerRun) waitRequests(t *testing.T, n int) ([]string, []time.Time) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if q, ts := r.fake.seen(); len(q) >= n {
			return q, ts
		}
		time.Sleep(5 * time.Millisecond)
	}
	q, _ := r.fake.seen()
	t.Fatalf("saw %d requests, want %d: %v", len(q), n, q)
	return nil, nil
}

func TestPollerAppliesSnapshotsAndLongPolls(t *testing.T) {
	r := startPoller(t, time.Millisecond, 5*time.Millisecond,
		reply{200, snapshotJSON(1, "a")},
		reply{304, ""},
		reply{200, snapshotJSON(2, "b")},
	)
	r.waitUpdate(t, 1)
	r.waitUpdate(t, 2)
	if _, ok := r.store.Current().Hook("b"); !ok {
		t.Fatal("hook b not applied")
	}
	queries, _ := r.waitRequests(t, 4)
	want := []string{"", "after_version=1&wait_s=1", "after_version=1&wait_s=1", "after_version=2&wait_s=1"}
	for i, q := range want {
		if queries[i] != q {
			t.Errorf("request %d query = %q, want %q", i, queries[i], q)
		}
	}
}

func TestPollerNoBackoffAfter304(t *testing.T) {
	r := startPoller(t, 200*time.Millisecond, time.Second,
		reply{200, snapshotJSON(1, "a")},
		reply{304, ""},
	)
	r.waitUpdate(t, 1)
	_, times := r.waitRequests(t, 3)
	if gap := times[2].Sub(times[1]); gap >= 200*time.Millisecond {
		t.Fatalf("waited %v after a 304; the next long-poll must start at once", gap)
	}
}

func TestPollerIgnoresStaleSnapshot(t *testing.T) {
	r := startPoller(t, time.Millisecond, 5*time.Millisecond,
		reply{200, snapshotJSON(2, "a")},
		reply{200, snapshotJSON(1, "old")},
	)
	r.waitUpdate(t, 2)
	queries, _ := r.waitRequests(t, 3)
	if queries[2] != "after_version=2&wait_s=1" {
		t.Fatalf("after a stale snapshot query = %q, want after_version=2", queries[2])
	}
	select {
	case v := <-r.updates:
		t.Fatalf("OnUpdate called for stale version %d", v)
	default:
	}
	if r.store.Current().Version() != 2 {
		t.Fatalf("store version = %d, want 2", r.store.Current().Version())
	}
}

func TestPollerBacksOffExponentiallyAndResets(t *testing.T) {
	min, max := 20*time.Millisecond, 50*time.Millisecond
	r := startPoller(t, min, max,
		reply{500, "boom"},
		reply{500, "boom"},
		reply{500, "boom"},
		reply{200, snapshotJSON(1, "a")},
		reply{500, "boom"},
	)
	r.waitUpdate(t, 1)
	_, times := r.waitRequests(t, 6)
	// Rand returns 1, so each pause is exactly the ceiling: 20, 40, 50 (capped) ms.
	wants := []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 50 * time.Millisecond}
	for i, want := range wants {
		gap := times[i+1].Sub(times[i])
		if gap < want || gap > want+150*time.Millisecond {
			t.Errorf("pause %d = %v, want about %v", i+1, gap, want)
		}
	}
	// After a success the next failure starts again at the minimum.
	if gap := times[5].Sub(times[4]); gap > min+150*time.Millisecond {
		t.Errorf("pause after success+failure = %v, want about %v (reset)", gap, min)
	}
}

func TestPollerBacksOffOnRejectedSnapshot(t *testing.T) {
	r := startPoller(t, 100*time.Millisecond, time.Second,
		reply{200, `{"version": 0}`},
		reply{200, snapshotJSON(1, "a")},
	)
	r.waitUpdate(t, 1)
	_, times := r.waitRequests(t, 2)
	if gap := times[1].Sub(times[0]); gap < 100*time.Millisecond {
		t.Fatalf("retried a rejected snapshot after %v; must back off", gap)
	}
	if !strings.Contains(r.logs.String(), "level=ERROR") || !strings.Contains(r.logs.String(), "snapshot rejected") {
		t.Fatalf("rejected snapshot not logged as error:\n%s", r.logs.String())
	}
}

func TestPollerKeepsRetryingWhenUnauthorized(t *testing.T) {
	r := startPoller(t, time.Millisecond, 5*time.Millisecond,
		reply{401, ""}, reply{401, ""}, reply{401, ""},
	)
	r.waitRequests(t, 4)
	if r.store.Current() != nil {
		t.Fatal("store must stay empty")
	}
	select {
	case err := <-r.done:
		t.Fatalf("Run returned %v; it must keep retrying", err)
	default:
	}
	if !strings.Contains(r.logs.String(), "control plane rejected the internal token") {
		t.Fatalf("unauthorized not logged clearly:\n%s", r.logs.String())
	}
}

func TestPollerStopsPromptlyOnCancel(t *testing.T) {
	r := startPoller(t, time.Millisecond, 5*time.Millisecond, reply{200, snapshotJSON(1, "a")})
	r.waitUpdate(t, 1)
	r.waitRequests(t, 2) // now blocked in a long-poll
	start := time.Now()
	r.cancel()
	select {
	case err := <-r.done:
		if err != context.Canceled {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
		r.done <- err // for the cleanup
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("Run took %v to stop", took)
	}
}

func TestPollerPausesAfterAStaleSnapshot(t *testing.T) {
	// A proxy that ignores after_version keeps answering with the same
	// version: the poller must not download it in a tight loop.
	r := startPoller(t, 100*time.Millisecond, time.Second,
		reply{200, snapshotJSON(1, "a")},
		reply{200, snapshotJSON(1, "a")},
	)
	r.waitUpdate(t, 1)
	_, times := r.waitRequests(t, 3)
	if gap := times[2].Sub(times[1]); gap < 100*time.Millisecond {
		t.Fatalf("refetched %v after a stale snapshot; want a pause of MinBackoff", gap)
	}
}
