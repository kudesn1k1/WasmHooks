// Package gateway admits operator invocations: it authenticates the
// operator, resolves the hook, validates the payload against the hook's
// input schema, resolves the tenant's binding and hands the call to an
// execproto.Executor with every version pinned.
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
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
)

var (
	// ErrNotReady: no config snapshot is loaded yet.
	ErrNotReady = errors.New("gateway: config not loaded")
	// ErrUnknownHook: the operator called a hook that does not exist.
	ErrUnknownHook = errors.New("gateway: unknown hook")
	// ErrInvalidPayload: the payload does not match the hook's input schema.
	ErrInvalidPayload = errors.New("gateway: payload does not match input schema")
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

// Options tune deadlines. Zero values take the defaults.
type Options struct {
	DefaultDeadlineExtra time.Duration // default 100ms on top of the hook timeout
	MaxDeadline          time.Duration // default 30s
}

func (o *Options) setDefaults() {
	if o.DefaultDeadlineExtra <= 0 {
		o.DefaultDeadlineExtra = 100 * time.Millisecond
	}
	if o.MaxDeadline <= 0 {
		o.MaxDeadline = 30 * time.Second
	}
}

// Gateway is safe for concurrent use.
type Gateway struct {
	cfg     *config.Store
	exec    execproto.Executor
	schemas *schema.Cache
	opts    Options
}

// New creates a gateway.
func New(cfg *config.Store, exec execproto.Executor, schemas *schema.Cache, opts Options) *Gateway {
	opts.setDefaults()
	return &Gateway{cfg: cfg, exec: exec, schemas: schemas, opts: opts}
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

// Authenticate reports whether token is a valid API key of this installation.
func (g *Gateway) Authenticate(token string) bool {
	v := g.cfg.Current()
	return v != nil && token != "" && v.AuthenticateToken(token)
}

// Invoke runs the hook for the tenant. Errors are request errors (not
// ready, unknown hook, invalid payload); every execution result, including
// failures of the platform, is a Response.
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

	binding, ok := view.Binding(req.TenantID, req.Hook)
	if !ok {
		return Response{Outcome: execproto.OutcomeNoHandler, Duration: time.Since(start)}, nil
	}

	deadline := req.Deadline
	if deadline <= 0 {
		deadline = hook.Timeout() + g.opts.DefaultDeadlineExtra
	}
	deadline = min(deadline, g.opts.MaxDeadline)
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	res, err := g.exec.Execute(ctx, execproto.ExecuteRequest{
		TenantID:       req.TenantID,
		Hook:           req.Hook,
		HookDefVersion: hook.DefVersion,
		ModuleHash:     binding.ModuleHash,
		ConfigVersion:  binding.ConfigVersion,
		Payload:        req.Payload,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		res = execproto.ExecuteResult{
			Outcome: execproto.OutcomeUnavailable,
			Reason:  execproto.ReasonInternal,
			Error:   "executor unreachable",
		}
	}
	resp := Response{
		Outcome:  res.Outcome,
		Reason:   res.Reason,
		Result:   res.Result,
		Effects:  res.Effects,
		Error:    res.Error,
		Duration: time.Since(start),
	}
	if scriptRan(res) {
		resp.ModuleHash = binding.ModuleHash
	}
	return resp, nil
}
