// Package gateway admits operator invocations: it authenticates the
// operator, resolves the hook, validates the payload against the hook's
// input schema, resolves the tenant's binding, applies the tenant's quotas
// and hands the call to an execproto.Executor with every version pinned.
//
// The gateway never imports the executor: the two are joined only through
// execproto, so they can run as separate processes (see internal/archtest).
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/gateway/quota"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
)

var (
	// ErrNotReady: no config snapshot is loaded yet.
	ErrNotReady = errors.New("gateway: config not loaded")
	// ErrUnknownHook: the operator called a hook that does not exist.
	ErrUnknownHook = errors.New("gateway: unknown hook")
	// ErrInvalidPayload: the payload does not match the hook's input schema.
	ErrInvalidPayload = errors.New("gateway: payload does not match input schema")
	// ErrExecutionUnknown means an Execute attempt may have started, but no result arrived.
	ErrExecutionUnknown = errors.New("execution result unknown")
)

// Request is one invocation as the operator asked for it.
type Request struct {
	Hook           string
	TenantID       string
	Payload        []byte        // JSON object
	Deadline       time.Duration // 0: hook timeout + DefaultDeadlineExtra
	IdempotencyKey string
}

// Response is the outcome returned to the operator.
type Response struct {
	Outcome    execproto.Outcome
	Reason     string
	Result     json.RawMessage
	Effects    []execproto.Effect
	Error      string
	ModuleHash string // set when a script was resolved
	Duration   time.Duration
}

// Options tune admission and deadlines. Zero values take the defaults.
type Options struct {
	DefaultDeadlineExtra time.Duration // default 100ms on top of the hook timeout
	MaxDeadline          time.Duration // default 30s
	BreakerThreshold     int           // consecutive guest failures, default 5
	BreakerCooldown      time.Duration // delay before a single probe, default 5s
	DisableBreaker       bool          // bypass the circuit in isolation/load experiments
	MaxInFlight          int           // includes waiters in the quota queues, default 256
}

func (o *Options) setDefaults() {
	if o.DefaultDeadlineExtra <= 0 {
		o.DefaultDeadlineExtra = 100 * time.Millisecond
	}
	if o.MaxDeadline <= 0 {
		o.MaxDeadline = 30 * time.Second
	}
	if o.BreakerThreshold <= 0 {
		o.BreakerThreshold = 5
	}
	if o.BreakerCooldown <= 0 {
		o.BreakerCooldown = 5 * time.Second
	}
	if o.MaxInFlight <= 0 {
		o.MaxInFlight = 256
	}
}

// Gateway is safe for concurrent use.
type Gateway struct {
	cfg      *config.Store
	exec     execproto.Executor
	schemas  *schema.Cache
	quotas   *quota.Manager
	opts     Options
	breaker  breaker
	inFlight chan struct{}
	metrics  counters
}

// New creates a gateway.
func New(cfg *config.Store, exec execproto.Executor, schemas *schema.Cache, opts Options) *Gateway {
	opts.setDefaults()
	return &Gateway{
		cfg: cfg, exec: exec, schemas: schemas, quotas: quota.New(nil), opts: opts,
		breaker:  breaker{pairs: make(map[breakerKey]*circuit)},
		inFlight: make(chan struct{}, opts.MaxInFlight),
		metrics:  counters{outcomes: make(map[string]map[string]uint64)},
	}
}

// scriptRan reports whether the outcome means the script started: the
// contract returns module_hash exactly then.
func scriptRan(res execproto.ExecuteResult) bool {
	switch res.Outcome {
	case execproto.OutcomeOK, execproto.OutcomeTimeout:
		return true
	case execproto.OutcomeHandlerError:
		return res.Reason != execproto.ReasonInvalidModule
	}
	return false
}

// tenantLimits returns the tenant's quotas from the snapshot. A tenant the
// snapshot does not list gets the defaults.
func tenantLimits(view *config.View, tenantID string) quota.Limits {
	t, ok := view.Tenant(tenantID)
	if !ok {
		return quota.Limits{Concurrency: config.DefaultConcurrencyLimit, Version: view.Version()}
	}
	return quota.Limits{RateRPS: t.RateLimitRPS, Concurrency: t.ConcurrencyLimit, Version: view.Version()}
}

// waitBudget bounds a wait before the script starts to the part of ctx's
// deadline beyond limit, the script's own time (review 1, I1). A wait that
// runs out leaves the script never started rather than started short.
func waitBudget(ctx context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	dl, ok := ctx.Deadline()
	if !ok {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, dl.Add(-limit))
}

// rejected maps a quota refusal. The script never started.
func rejected(err error) Response {
	switch {
	case errors.Is(err, quota.ErrRateLimited):
		return Response{Outcome: execproto.OutcomeQuotaExceeded, Reason: execproto.ReasonRateLimit}
	case errors.Is(err, quota.ErrConcurrency):
		return Response{Outcome: execproto.OutcomeQuotaExceeded, Reason: execproto.ReasonConcurrencyLimit}
	case errors.Is(err, context.Canceled):
		return Response{Outcome: execproto.OutcomeUnavailable, Reason: execproto.ReasonCallerCanceled,
			Error: "caller went away before the script started"}
	default:
		return Response{Outcome: execproto.OutcomeUnavailable, Reason: execproto.ReasonDeadline,
			Error: "deadline expired before the script started"}
	}
}

// ConfigLoaded reports whether a config snapshot has been applied. Until then
// nothing can be served, not even authentication: API keys arrive in the
// snapshot too.
func (g *Gateway) ConfigLoaded() bool { return g.cfg.Current() != nil }

// Authenticate reports whether token is a valid API key of this installation.
func (g *Gateway) Authenticate(token string) bool {
	v := g.cfg.Current()
	return v != nil && token != "" && v.AuthenticateToken(token)
}

// Invoke returns a Response for a known outcome. A transport failure after
// dispatch returns ErrExecutionUnknown because the script may have run.
func (g *Gateway) Invoke(ctx context.Context, req Request) (Response, error) {
	start := time.Now()
	view := g.cfg.Current()
	if view == nil {
		return Response{}, ErrNotReady
	}
	hook, ok := view.Hook(req.Hook)
	if !ok {
		return Response{}, fmt.Errorf("%w: %q", ErrUnknownHook, req.Hook)
	}
	in, err := g.schemas.Get(hook.SchemaKey("input"), hook.InputSchema)
	if err != nil {
		return Response{}, fmt.Errorf("input schema of %s: %w", hook.Name, err)
	}
	if err := in.Validate(req.Payload); err != nil {
		return Response{}, fmt.Errorf("%w: %w", ErrInvalidPayload, err)
	}

	resp, err := g.invokeValidated(ctx, req, view, hook)
	g.record(req.TenantID, resp, err)
	resp.Duration = time.Since(start)
	return resp, err
}

func (g *Gateway) invokeValidated(ctx context.Context, req Request, view *config.View, hook config.HookDef) (Response, error) {
	binding, ok := view.Binding(req.TenantID, req.Hook)
	if !ok {
		return Response{Outcome: execproto.OutcomeNoHandler}, nil
	}
	select {
	case g.inFlight <- struct{}{}:
		defer func() { <-g.inFlight }()
	default:
		return Response{Outcome: execproto.OutcomeUnavailable, Reason: execproto.ReasonOverloaded,
			Error: "gateway in-flight limit reached"}, nil
	}
	finish, allowed := g.enterCircuit(breakerKey{tenant: req.TenantID, hook: req.Hook, hash: binding.ModuleHash,
		def: hook.DefVersion, config: binding.ConfigVersion})
	if !allowed {
		return Response{Outcome: execproto.OutcomeUnavailable, Reason: reasonBreakerOpen,
			Error: "circuit breaker open for tenant hook"}, nil
	}
	var res execproto.ExecuteResult
	defer func() { finish(res) }()

	deadline := req.Deadline
	if deadline <= 0 {
		deadline = hook.Timeout() + g.opts.DefaultDeadlineExtra
	}
	deadline = min(deadline, g.opts.MaxDeadline)
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	// Quotas count from here to the executor's answer. Waiting for room may
	// spend only the deadline beyond the hook's limit, so an admitted script
	// keeps its full time; with a shorter deadline there is no waiting.
	waitCtx, cancelWait := waitBudget(ctx, hook.Timeout())
	release, err := g.quotas.Admit(waitCtx, req.TenantID, tenantLimits(view, req.TenantID))
	cancelWait()
	if err != nil {
		return rejected(err), nil
	}
	defer release()

	res, err = g.exec.Execute(ctx, execproto.ExecuteRequest{
		TenantID:       req.TenantID,
		Hook:           req.Hook,
		HookDefVersion: hook.DefVersion,
		ModuleHash:     binding.ModuleHash,
		ConfigVersion:  binding.ConfigVersion,
		Payload:        req.Payload,
		IdempotencyKey: req.IdempotencyKey,
	})
	// Executor diagnostics distinguish a caller who canceled while the guest
	// ran from a cancellation before start. Only the latter may return 503.
	if err != nil || (res.Outcome == execproto.OutcomeUnavailable && res.ExecNanos > 0) {
		res = execproto.ExecuteResult{}
		return Response{}, ErrExecutionUnknown
	}
	resp := Response{
		Outcome: res.Outcome,
		Reason:  res.Reason,
		Result:  res.Result,
		Effects: res.Effects,
		Error:   res.Error,
	}
	if resp.Outcome == execproto.OutcomeUnavailable && resp.Error == "" {
		resp.Error = "executor unavailable"
		if resp.Reason != "" {
			resp.Error += ": " + resp.Reason
		}
	}
	if scriptRan(res) {
		resp.ModuleHash = binding.ModuleHash
	}
	return resp, nil
}
