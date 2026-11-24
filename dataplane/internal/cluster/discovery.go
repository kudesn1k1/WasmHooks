package cluster

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/cluster/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type Options struct {
	Address                                 string // headless Service host:port
	Token                                   string
	Interval, HealthTimeout, RetryMinBudget time.Duration
	OnChange                                func() // runs after publishing the new ring, outside the lock
}

type Stats struct {
	Attempts          map[string]uint64 `json:"attempts"`
	RetiredAttempts   uint64            `json:"retired_attempts"` // attempts folded out of the per-executor map
	Retries           uint64            `json:"retries"`
	SuccessfulRetries uint64            `json:"successful_retries"`
}

type Snapshot struct {
	Members []string
	Owners  []string
	Stats   Stats
}

type peer struct {
	conn    *grpc.ClientConn
	client  wire.ExecutorClient
	active  int
	retired bool
}

type Cluster struct {
	opts    Options
	resolve func(context.Context) ([]string, error)
	// Run owns the address map. The lock protects routing, in-flight
	// references and counters shared with Execute.
	addresses map[string]*peer
	mu        sync.Mutex
	ring      ring
	members   map[string]*peer
	stats     Stats
	departed  []string // oldest departure first, only IDs with counted attempts
}

func New(opts Options) (*Cluster, error) {
	host, port, err := net.SplitHostPort(opts.Address)
	portNum, portErr := strconv.Atoi(port)
	if err != nil || host == "" || portErr != nil || portNum < 1 || portNum > 65535 {
		return nil, fmt.Errorf("invalid executor discovery address %q", opts.Address)
	}
	if opts.Token == "" {
		return nil, errors.New("executor discovery needs an internal token")
	}
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	if opts.HealthTimeout <= 0 {
		opts.HealthTimeout = 400 * time.Millisecond
	}
	if opts.RetryMinBudget <= 0 {
		opts.RetryMinBudget = 10 * time.Millisecond
	}
	c := &Cluster{opts: opts, addresses: make(map[string]*peer), stats: Stats{Attempts: make(map[string]uint64)}}
	c.resolve = func(ctx context.Context) ([]string, error) {
		ips, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return nil, err
		}
		addresses := make([]string, 0, len(ips))
		for _, ip := range ips {
			addresses = append(addresses, net.JoinHostPort(ip, port))
		}
		return addresses, nil
	}
	return c, nil
}

func (c *Cluster) Members() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.ring.ids)
}

func (c *Cluster) Owners(tenant, hook string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ring.owners(tenant, hook)
}

func (c *Cluster) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats.clone()
}

func (s Stats) clone() Stats {
	s.Attempts = maps.Clone(s.Attempts)
	return s
}

func (c *Cluster) Snapshot(tenant, hook string) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{
		Members: slices.Clone(c.ring.ids),
		Owners:  c.ring.owners(tenant, hook),
		Stats:   c.stats.clone(),
	}
}

func (c *Cluster) authenticated(ctx context.Context) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("authorization", "Bearer "+c.opts.Token)
	return metadata.NewOutgoingContext(ctx, md)
}

// Run refreshes immediately and until cancellation. The same channels serve
// health and Execute, keeping HTTP/2 connections warm between checks.
func (c *Cluster) Run(ctx context.Context) {
	defer c.close()
	ticker := time.NewTicker(c.opts.Interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		c.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Cluster) refresh(ctx context.Context) {
	lookup, cancel := context.WithTimeout(ctx, c.opts.HealthTimeout)
	addresses, err := c.resolve(lookup)
	cancel()
	var dnsErr *net.DNSError
	if err != nil && !(errors.As(err, &dnsErr) && dnsErr.IsNotFound) {
		// DNS failure alone cannot establish executor failure. Previously
		// discovered addresses must still pass this round's health check.
		addresses = slices.Collect(maps.Keys(c.addresses))
	}
	next := make(map[string]*peer, len(addresses))
	for _, addr := range addresses {
		if next[addr] != nil {
			continue
		}
		p := c.addresses[addr]
		if p == nil {
			conn, err := grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithDisableRetry(), grpc.WithStatsHandler(dispatchStats{}),
				grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(messageLimit), grpc.MaxCallSendMsgSize(messageLimit)))
			if err != nil {
				continue
			}
			p = &peer{conn: conn, client: wire.NewExecutorClient(conn)}
		}
		next[addr] = p
	}
	type member struct {
		id string
		p  *peer
	}
	found := make(chan member, len(next))
	var probes sync.WaitGroup
	for _, p := range next {
		probes.Go(func() {
			probe, cancel := context.WithTimeout(ctx, c.opts.HealthTimeout)
			defer cancel()
			resp, err := p.client.Health(c.authenticated(probe), &wire.HealthRequest{}, grpc.WaitForReady(true))
			if err == nil && resp.Ready && resp.ExecutorId != "" {
				found <- member{resp.ExecutorId, p}
			}
		})
	}
	probes.Wait()
	close(found)
	healthy := make(map[string]*peer, len(next))
	for m := range found {
		healthy[m.id] = m.p
	}
	ids := slices.Sorted(maps.Keys(healthy))
	c.mu.Lock()
	changed := !slices.Equal(ids, c.ring.ids)
	if changed {
		c.retainAttempts(healthy)
		c.ring = newRing(ids)
	}
	c.members = healthy
	for addr, p := range c.addresses {
		if next[addr] == nil {
			c.retire(p)
		}
	}
	c.addresses = next
	c.mu.Unlock()
	if changed && c.opts.OnChange != nil {
		c.opts.OnChange()
	}
}

// Keep the per-executor breakdown for current members and recent departures.
// Holding the same lock as acquire makes each retirement and increment atomic
// with respect to Stats snapshots.
func (c *Cluster) retainAttempts(healthy map[string]*peer) {
	const recentDepartures = 16
	c.departed = slices.DeleteFunc(c.departed, func(id string) bool { return healthy[id] != nil })
	for _, id := range c.ring.ids {
		if healthy[id] != nil || c.stats.Attempts[id] == 0 {
			continue
		}
		if len(c.departed) == recentDepartures {
			oldest := c.departed[0]
			c.stats.RetiredAttempts += c.stats.Attempts[oldest]
			delete(c.stats.Attempts, oldest)
			c.departed = slices.Delete(c.departed, 0, 1)
		}
		c.departed = append(c.departed, id)
	}
}

// retire and release close a removed address after its last running call.
// Discovery must not destroy an in-flight response during a DNS transition.
func (c *Cluster) retire(p *peer) {
	p.retired = true
	if p.active == 0 {
		p.conn.Close()
	}
}

func (c *Cluster) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.addresses {
		c.retire(p)
	}
	c.addresses = nil
	c.retainAttempts(nil)
	c.members = nil
	c.ring = ring{}
}
