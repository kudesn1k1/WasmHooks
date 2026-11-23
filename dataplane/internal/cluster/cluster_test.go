package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/cluster/wire"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type executeFunc func(context.Context, execproto.ExecuteRequest) (execproto.ExecuteResult, error)

func (f executeFunc) Execute(ctx context.Context, req execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
	return f(ctx, req)
}

type node struct {
	id, address string
	server      *grpc.Server
	ready       atomic.Bool
	run         executeFunc
}

func startNode(t *testing.T, id string) *node {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	n := &node{id: id, address: ln.Addr().String()}
	n.ready.Store(true)
	n.server = NewServer(executeFunc(func(ctx context.Context, req execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
		return n.run(ctx, req)
	}), id, "internal", n.ready.Load)
	go n.server.Serve(ln)
	t.Cleanup(n.server.Stop)
	return n
}

func testCluster(t *testing.T, token string, nodes ...*node) *Cluster {
	t.Helper()
	c, err := New(Options{Address: "executors:9090", Token: token, HealthTimeout: 2 * time.Second, RetryMinBudget: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	c.resolve = func(context.Context) ([]string, error) {
		var addresses []string
		for _, n := range nodes {
			addresses = append(addresses, n.address)
		}
		return addresses, nil
	}
	t.Cleanup(c.close)
	c.refresh(t.Context())
	return c
}

func TestDiscoveryRemovesOnlyUnhealthyOwners(t *testing.T) {
	a, b, d := startNode(t, "a"), startNode(t, "b"), startNode(t, "d")
	c := testCluster(t, "internal", a, b, d)
	if got := c.Members(); !slices.Equal(got, []string{"a", "b", "d"}) {
		t.Fatal(got)
	}
	initial := make(map[string]string)
	used := make(map[string]int)
	for i := 0; i < 500; i++ {
		tenant := fmt.Sprint(i)
		owners := c.Owners(tenant, "checkout")
		if len(owners) != 3 {
			t.Fatal(owners)
		}
		initial[tenant] = owners[0]
		used[owners[0]]++
	}
	if len(used) != 3 {
		t.Fatal("missing owner", used)
	}
	b.ready.Store(false)
	c.refresh(t.Context())
	if got := c.Members(); !slices.Equal(got, []string{"a", "d"}) {
		t.Fatal(got)
	}
	for tenant, was := range initial {
		now := c.Owners(tenant, "checkout")
		if slices.Contains(now, "b") || was != "b" && was != now[0] {
			t.Fatalf("%s: %s -> %v", tenant, was, now)
		}
	}
	b.ready.Store(true)
	c.refresh(t.Context())
	for tenant, was := range initial {
		if now := c.Owners(tenant, "checkout")[0]; now != was {
			t.Fatalf("%s: %s -> %s", tenant, was, now)
		}
	}
	if got := testCluster(t, "wrong", a).Members(); len(got) != 0 {
		t.Fatal("unauthenticated discovery", got)
	}
}

func TestLostResponseRetriesPinnedCallOnAnotherNode(t *testing.T) {
	a, b := startNode(t, "a"), startNode(t, "b")
	c := testCluster(t, "internal", a, b)
	req := execproto.ExecuteRequest{TenantID: "shop", Hook: "checkout", HookDefVersion: 17, ModuleHash: "hash", ConfigVersion: 9007199254740993, Payload: []byte(`{"total":100}`), IdempotencyKey: "order"}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	primary := c.Owners(req.TenantID, req.Hook)[0]
	want := execproto.ExecuteResult{Outcome: execproto.OutcomeOK, Result: []byte(`{"discount":7}`), Effects: []execproto.Effect{{Type: "mark", Key: "order", Payload: []byte(`{}`)}}, ExecNanos: 33, ColdStart: true, Logs: []string{"pricing"}}
	var mu sync.Mutex
	var calls []string
	for _, n := range []*node{a, b} {
		n.run = func(sc context.Context, r execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
			mu.Lock()
			calls = append(calls, n.id)
			mu.Unlock()
			if !reflect.DeepEqual(req, r) {
				t.Errorf("changed pinned request: %+v", r)
			}
			d, ok := sc.Deadline()
			original, _ := ctx.Deadline()
			if !ok || d.Sub(original).Abs() > 30*time.Millisecond {
				t.Errorf("deadline changed: %v vs %v", d, original)
			}
			if n.id == primary {
				n.server.Stop()
			} // execution finished, TCP closes before the response
			return want, nil
		}
	}
	got, err := c.Execute(ctx, req)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("result=%+v err=%v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(calls, c.Owners(req.TenantID, req.Hook)) {
		t.Fatalf("attempts: %v", calls)
	}
	s := c.Stats()
	if s.Attempts["a"] != 1 || s.Attempts["b"] != 1 || s.Retries != 1 || s.SuccessfulRetries != 1 {
		t.Fatal(s)
	}
}

func TestRetryBoundaries(t *testing.T) {
	refusal := func(reason string) execproto.ExecuteResult {
		return execproto.ExecuteResult{Outcome: execproto.OutcomeUnavailable, Reason: reason}
	}
	ok := execproto.ExecuteResult{Outcome: execproto.OutcomeOK}
	for _, tc := range []struct {
		name          string
		first, second execproto.ExecuteResult // zero simulates a lost answer
		short         bool
		attempts      int
		want          execproto.ExecuteResult
		unknown       bool
	}{
		{"overload retries", refusal(execproto.ReasonOverloaded), ok, false, 2, ok, false},
		{"pool busy retries", refusal(execproto.ReasonPoolSaturated), ok, false, 2, ok, false},
		{"two refusals", refusal(execproto.ReasonOverloaded), refusal(execproto.ReasonOverloaded), false, 2, refusal(execproto.ReasonOverloaded), false},
		{"short budget after refusal", refusal(execproto.ReasonOverloaded), ok, true, 1, refusal(execproto.ReasonOverloaded), false},
		{"short budget after lost answer", execproto.ExecuteResult{}, ok, true, 1, execproto.ExecuteResult{}, true},
		{"lost then refused", execproto.ExecuteResult{}, refusal(execproto.ReasonOverloaded), false, 2, execproto.ExecuteResult{}, true},
		{"both lost", execproto.ExecuteResult{}, execproto.ExecuteResult{}, false, 2, execproto.ExecuteResult{}, true},
		{"refused then lost", refusal(execproto.ReasonPoolSaturated), execproto.ExecuteResult{}, false, 2, execproto.ExecuteResult{}, true},
		{"canceled after start", execproto.ExecuteResult{Outcome: execproto.OutcomeUnavailable, Reason: execproto.ReasonCallerCanceled, ExecNanos: 1}, ok, false, 1, execproto.ExecuteResult{}, true},
		{"overload reported after start", execproto.ExecuteResult{Outcome: execproto.OutcomeUnavailable, Reason: execproto.ReasonOverloaded, ExecNanos: 1}, ok, false, 1, execproto.ExecuteResult{}, true},
		{"quota", execproto.ExecuteResult{Outcome: execproto.OutcomeQuotaExceeded}, ok, false, 1, execproto.ExecuteResult{Outcome: execproto.OutcomeQuotaExceeded}, false},
		{"guest error", execproto.ExecuteResult{Outcome: execproto.OutcomeHandlerError}, ok, false, 1, execproto.ExecuteResult{Outcome: execproto.OutcomeHandlerError}, false},
		{"timeout", execproto.ExecuteResult{Outcome: execproto.OutcomeTimeout}, ok, false, 1, execproto.ExecuteResult{Outcome: execproto.OutcomeTimeout}, false},
		{"stale config", refusal(execproto.ReasonStaleConfig), ok, false, 1, refusal(execproto.ReasonStaleConfig), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b, d := startNode(t, "a"), startNode(t, "b"), startNode(t, "d")
			c := testCluster(t, "internal", a, b, d)
			req := execproto.ExecuteRequest{TenantID: "shop", Hook: "checkout"}
			first := c.Owners(req.TenantID, req.Hook)[0]
			var attempts atomic.Int32
			for _, n := range []*node{a, b, d} {
				res := tc.second
				if n.id == first {
					res = tc.first
				}
				n.run = func(context.Context, execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
					attempts.Add(1)
					if res.Outcome == "" {
						return res, errors.New("response unavailable")
					}
					return res, nil
				}
			}
			if tc.short {
				c.opts.RetryMinBudget = time.Minute
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			got, err := c.Execute(ctx, req)
			if (err != nil) != tc.unknown || !reflect.DeepEqual(got, tc.want) || int(attempts.Load()) != tc.attempts {
				t.Fatalf("result=%+v err=%v attempts=%d", got, err, attempts.Load())
			}
		})
	}
}

func TestDeadlineBeforeDispatch(t *testing.T) {
	a := startNode(t, "a")
	c := testCluster(t, "internal", a)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	res, err := c.Execute(ctx, execproto.ExecuteRequest{})
	if err != nil || res.Outcome != execproto.OutcomeUnavailable || res.Reason != execproto.ReasonDeadline || c.Stats().Attempts["a"] != 0 {
		t.Fatalf("%+v %v %+v", res, err, c.Stats())
	}
}

func TestExecuteRequiresInternalCredential(t *testing.T) {
	n := startNode(t, "private")
	conn, err := grpc.NewClient("passthrough:///"+n.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rpc := wire.NewExecutorClient(conn)
	for _, values := range [][]string{{}, {"Bearer other"}, {"Bearer internal", "Bearer other"}} {
		md := metadata.MD{"authorization": values}
		ctx := metadata.NewOutgoingContext(t.Context(), md)
		if _, err := rpc.Execute(ctx, &wire.ExecuteRequest{}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("credential rejected with %v", err)
		}
	}
	n.ready.Store(false)
	ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer internal")
	reply, err := rpc.Execute(ctx, &wire.ExecuteRequest{})
	if err != nil || reply.Outcome != "unavailable" {
		t.Fatalf("draining executor: %v %v", reply, err)
	}
}

func TestRunPublishesAndWithdrawsReadiness(t *testing.T) {
	n := startNode(t, "pod")
	changed := make(chan struct{}, 4)
	c, err := New(Options{Address: n.address, Token: "internal", Interval: 5 * time.Millisecond, OnChange: func() { changed <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitChange := func() {
		t.Helper()
		select {
		case <-changed:
		case <-time.After(3 * time.Second):
			t.Fatal("discovery did not publish a change")
		}
	}
	waitChange()
	if got := c.Owners("shop", "checkout"); !slices.Equal(got, []string{"pod"}) {
		t.Fatal(got)
	}
	n.ready.Store(false)
	waitChange()
	res, err := c.Execute(t.Context(), execproto.ExecuteRequest{})
	if err != nil || res.Outcome != execproto.OutcomeUnavailable || len(c.Stats().Attempts) != 0 {
		t.Fatalf("%+v %v %+v", res, err, c.Stats())
	}
}

func TestCancellationReachesRunningExecutor(t *testing.T) {
	n := startNode(t, "pod")
	entered, stopped := make(chan struct{}), make(chan struct{})
	n.run = func(ctx context.Context, _ execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
		close(entered)
		<-ctx.Done()
		close(stopped)
		return execproto.ExecuteResult{}, ctx.Err()
	}
	c := testCluster(t, "internal", n)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := c.Execute(ctx, execproto.ExecuteRequest{}); result <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("executor did not start")
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancellation after dispatch must be unknown")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("executor did not see cancellation")
	}
}

func TestDiscoveryPreservesInflightResponseOnAddressRemoval(t *testing.T) {
	n := startNode(t, "pod")
	entered, finish := make(chan struct{}), make(chan struct{})
	n.run = func(context.Context, execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
		close(entered)
		<-finish
		return execproto.ExecuteResult{Outcome: execproto.OutcomeOK}, nil
	}
	c := testCluster(t, "internal", n)
	result := make(chan error, 1)
	go func() {
		res, err := c.Execute(t.Context(), execproto.ExecuteRequest{})
		if err == nil && res.Outcome != execproto.OutcomeOK {
			err = fmt.Errorf("wrong result: %+v", res)
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	c.resolve = func(context.Context) ([]string, error) { return nil, nil }
	c.refresh(t.Context())
	close(finish)
	if len(c.Members()) != 0 {
		t.Fatalf("removed address still assigned: %v", c.Members())
	}
	if err := <-result; err != nil {
		t.Fatal("response lost while removing DNS entry", err)
	}
}

func TestDNSFailureRechecksHealthAndEmptyAnswerWithdrawsAddresses(t *testing.T) {
	a, b := startNode(t, "a"), startNode(t, "b")
	c := testCluster(t, "internal", a, b)
	b.ready.Store(false)
	c.resolve = func(context.Context) ([]string, error) {
		return nil, &net.DNSError{IsTemporary: true, Err: "temporary DNS failure"}
	}
	c.refresh(t.Context())
	if got := c.Members(); !slices.Equal(got, []string{"a"}) {
		t.Fatal("DNS outage should retain previously known, currently healthy executors", got)
	}
	c.resolve = func(context.Context) ([]string, error) {
		return nil, &net.DNSError{IsNotFound: true, Err: "no records"}
	}
	c.refresh(t.Context())
	if got := c.Members(); len(got) != 0 {
		t.Fatal("headless Service with no endpoints still routes to", got)
	}
}

func TestEstablishedDiscoveryButConnectionRefusedBeforeDispatch(t *testing.T) {
	n := startNode(t, "gone")
	c := testCluster(t, "internal", n)
	// The last health round still advertises the node. Wait until gRPC
	// has observed that its existing HTTP/2 connection closed.
	n.server.Stop()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	conn := c.addresses[n.address].conn
	if conn.GetState() == connectivity.Ready && !conn.WaitForStateChange(ctx, connectivity.Ready) {
		t.Fatal("HTTP/2 connection did not close")
	}
	res, err := c.Execute(ctx, execproto.ExecuteRequest{})
	if err != nil || res.Outcome != execproto.OutcomeUnavailable {
		t.Fatalf("dial failure must be a confirmed no-start: %+v, %v", res, err)
	}
}

func TestRetainRecentDepartedAttempts(t *testing.T) {
	const retained = 16
	c := testCluster(t, "internal")
	var returning *node
	// Each generation replaces the previous pod as the only DNS answer.
	for generation := range retained + 3 {
		id := fmt.Sprintf("pod-%02d", generation)
		n := startNode(t, id)
		if generation == 2 {
			returning = n
		}
		n.run = func(context.Context, execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
			return execproto.ExecuteResult{Outcome: execproto.OutcomeOK}, nil
		}
		c.resolve = func(context.Context) ([]string, error) { return []string{n.address}, nil }
		c.refresh(t.Context())
		if res, err := c.Execute(t.Context(), execproto.ExecuteRequest{}); err != nil || res.Outcome != execproto.OutcomeOK {
			t.Fatalf("generation %d: %+v, %v", generation, res, err)
		}
		s := c.Stats()
		total := s.RetiredAttempts
		for _, count := range s.Attempts {
			total += count
		}
		if total != uint64(generation+1) {
			t.Fatalf("generation %d recorded %d total attempts", generation, total)
		}
		if len(s.Attempts) != min(generation+1, retained+1) {
			t.Fatalf("generation %d counts %d executor IDs", generation, len(s.Attempts))
		}
		for i := max(0, generation-retained); i <= generation; i++ {
			if got := s.Attempts[fmt.Sprintf("pod-%02d", i)]; got != 1 {
				t.Fatalf("generation %d lost recent pod %d", generation, i)
			}
		}
	}
	// At this point pod-02 is the oldest retained ID. Rejoining promotes
	// it back to the active breakdown before the latest departure is added.
	c.resolve = func(context.Context) ([]string, error) { return []string{returning.address}, nil }
	c.refresh(t.Context())
	if res, err := c.Execute(t.Context(), execproto.ExecuteRequest{}); err != nil || res.Outcome != execproto.OutcomeOK {
		t.Fatalf("rejoined pod: %+v %v", res, err)
	}
	s := c.Stats()
	if s.Attempts[returning.id] != 2 || s.RetiredAttempts != 2 {
		t.Fatalf("rejoined pod lost its attempts: %+v", s)
	}
}
