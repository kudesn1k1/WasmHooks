package load

import (
	"cmp"
	"context"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"
)

// sample is one finished call.
type sample struct {
	outcome string
	took    time.Duration
}

// generator is the open-model load of one tenant: calls leave on schedule,
// each in its own goroutine, and a slow answer does not delay the next call.
// Every tenant has its own client, so one tenant's connections and queueing
// never hold up another's calls on the client side.
type generator struct {
	tenant string
	rps    int
	caller *caller
}

func newGenerator(cfg Config, env Env, tenant string, rps int) (*generator, error) {
	dial, err := fixedDialer(cfg.DataPlane)
	if err != nil {
		return nil, err
	}
	// About one connection per call per second, capped: if the data plane is
	// slower than the load, calls wait for a connection instead of opening
	// thousands (each dial holds an OS thread on Windows, and the runtime
	// dies at 10000). The wait counts in that call's latency.
	conns := min(max(rps, 16), 512)
	tr := &http.Transport{
		DialContext:         dial,
		MaxIdleConns:        conns,
		MaxIdleConnsPerHost: conns,
		MaxConnsPerHost:     conns,
		IdleConnTimeout:     90 * time.Second,
	}
	return &generator{
		tenant: tenant,
		rps:    rps,
		caller: &caller{
			client:    &http.Client{Transport: tr, Timeout: cfg.CallTimeout},
			dataPlane: cfg.DataPlane,
			key:       env.APIKey,
			hook:      env.Hook,
		},
	}, nil
}

// fixedDialer dials the data plane's address resolved once, now. With a
// name like localhost every new connection would resolve it again, and on
// Windows each resolution holds an OS thread: thousands of calls in flight
// at the noisy rate exhaust the runtime's thread limit. IPv4 is tried first
// because compose publishes the port on 127.0.0.1 only.
func fixedDialer(baseURL string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	ips, err := net.LookupIP(u.Hostname())
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(ips, func(a, b net.IP) int {
		return cmp.Compare(len(b.To4()), len(a.To4())) // IPv4 first
	})
	var d net.Dialer
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		var last error
		for _, ip := range ips {
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}, nil
}

// run sends rps calls per second for warmup+measure and returns the samples
// of the measured part only. It returns once every call has an answer.
//
// A ticker only wakes the loop (its resolution is coarse on some systems);
// the schedule is call i at start + i/rps, and a wakeup sends every call that
// is due, so a late wakeup is caught up instead of lowering the rate.
func (g *generator) run(ctx context.Context, warmup, measure time.Duration) []sample {
	total := int(float64(g.rps) * (warmup + measure).Seconds())
	skip := int(float64(g.rps) * warmup.Seconds())
	samples := make([]sample, total)
	var wg sync.WaitGroup

	start := time.Now()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for sent := 0; sent < total; {
		select {
		case <-ctx.Done():
			sent = total
			continue
		case <-tick.C:
		}
		due := min(total, int(time.Since(start).Seconds()*float64(g.rps))+1)
		for ; sent < due; sent++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				outcome, took := g.caller.call(ctx, g.tenant)
				samples[i] = sample{outcome, took}
			}(sent)
		}
	}
	wg.Wait()
	return samples[skip:]
}

// Phase is the measured result of one tenant in one phase.
type Phase struct {
	Tenant, Name string
	N            int
	P50, P99     time.Duration
	Max          time.Duration
	Outcomes     map[string]int
}

func summarize(tenant, name string, samples []sample) Phase {
	p := Phase{Tenant: tenant, Name: name, N: len(samples), Outcomes: map[string]int{}}
	took := make([]time.Duration, len(samples))
	for i, s := range samples {
		took[i] = s.took
		p.Outcomes[s.outcome]++
	}
	slices.Sort(took)
	if len(took) > 0 {
		p.P50, p.P99, p.Max = percentile(took, 0.50), percentile(took, 0.99), took[len(took)-1]
	}
	return p
}

// percentile is the nearest-rank percentile of the sorted durations.
func percentile(sorted []time.Duration, q float64) time.Duration {
	rank := int(q*float64(len(sorted)) + 0.999999999)
	return sorted[min(max(rank, 1), len(sorted))-1]
}

// Result holds the three series of a run.
type Result struct {
	AQuiet, ANoisy, BNoisy Phase
}

// Run drives both phases. In the quiet phase merchant-a calls alone; in the
// noisy phase it makes the same calls while merchant-b makes cfg.BRPS per
// second. Each phase starts with an unmeasured warmup at the phase's load.
// progress, if not nil, is told when a phase starts. The error is a data plane
// address that does not resolve.
func Run(ctx context.Context, cfg Config, env Env, progress func(string)) (Result, error) {
	note := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	a, err := newGenerator(cfg, env, env.TenantA, cfg.ARPS)
	if err != nil {
		return Result{}, err
	}

	note("phase quiet: merchant-a alone")
	var res Result
	res.AQuiet = summarize(env.TenantA, "quiet", a.run(ctx, cfg.Warmup, cfg.Phase))

	note("phase noisy: merchant-a plus merchant-b")
	b, err := newGenerator(cfg, env, env.TenantB, cfg.BRPS)
	if err != nil {
		return Result{}, err
	}
	var bSamples []sample
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		bSamples = b.run(ctx, cfg.Warmup, cfg.Phase)
	}()
	aNoisy := a.run(ctx, cfg.Warmup, cfg.Phase)
	wg.Wait()
	res.ANoisy = summarize(env.TenantA, "noisy", aNoisy)
	res.BNoisy = summarize(env.TenantB, "noisy", bSamples)
	return res, nil
}
