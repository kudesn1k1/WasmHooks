// Package executor runs tenant scripts: it resolves the pinned hook
// definition and module, compiles modules once per (hash, hook definition),
// leases a tenant-scoped instance, calls the script and checks its output
// against the hook contract. It implements execproto.Executor.
package executor

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/observe"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/pool"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
)

// Options wire the executor's dependencies. Zero limits take the defaults.
type Options struct {
	Runtime        sandbox.Runtime
	Modules        modstore.Store
	Config         *config.Store
	Pools          *pool.Manager
	Schemas        *schema.Cache
	Sink           observe.Sink
	ExecutorID     string
	MaxOutputBytes int // default 1 MiB
	MaxErrorBytes  int // default 1 KiB
	// CompileTimeout bounds fetching and compiling a module where the
	// module store and runtime honour the context; the wazero compiler
	// itself does not. Default 30s.
	CompileTimeout time.Duration
}

func (o *Options) setDefaults() {
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = 1 << 20
	}
	if o.MaxErrorBytes <= 0 {
		o.MaxErrorBytes = 1 << 10
	}
	if o.CompileTimeout <= 0 {
		o.CompileTimeout = 30 * time.Second
	}
}

// modKey identifies a compiled module: the bytes and everything the module
// is compiled under. Keying by the spec rather than by (hook, def_version)
// means hooks with identical limits share compiled code, hooks with
// different limits never do, and a definition bump that keeps the limits
// reuses the module.
type modKey struct {
	hash string
	spec string
}

func specOf(hook config.HookDef) sandbox.ModuleSpec {
	return sandbox.ModuleSpec{
		MemoryMaxPages:       hook.MemoryMaxPages,
		Timeout:              hook.Timeout(),
		AllowedHostFunctions: hook.AllowedHostFunctions,
	}
}

func specDigest(s sandbox.ModuleSpec) string {
	fns := slices.Clone(s.AllowedHostFunctions)
	slices.Sort(fns)
	return strconv.FormatUint(uint64(s.MemoryMaxPages), 10) + "/" + s.Timeout.String() + "/" + strings.Join(fns, ",")
}

type hookKey struct {
	name       string
	defVersion int64
}

// Executor is safe for concurrent use.
type Executor struct {
	opts Options

	mu      sync.Mutex
	modules map[modKey]sandbox.Module
	invalid map[modKey]error // modules that failed validation stay failed
	hooks   map[hookKey]config.HookDef
	closed  bool

	compiles singleflight.Group
}

var _ execproto.Executor = (*Executor)(nil)

// New creates an executor. Call Preload before serving traffic.
func New(opts Options) *Executor {
	opts.setDefaults()
	return &Executor{
		opts:    opts,
		modules: make(map[modKey]sandbox.Module),
		invalid: make(map[modKey]error),
		hooks:   make(map[hookKey]config.HookDef),
	}
}

// failure is an execution that did not produce ok.
type failure struct {
	outcome execproto.Outcome
	reason  string
	err     error
}

func fail(outcome execproto.Outcome, reason string, err error) *failure {
	return &failure{outcome: outcome, reason: reason, err: err}
}

// Execute runs one invocation. It never returns a transport error.
func (e *Executor) Execute(ctx context.Context, req execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
	start := time.Now()
	res := e.execute(ctx, req)
	e.opts.Sink.Record(observe.Invocation{
		TS:             start,
		TenantID:       req.TenantID,
		Hook:           req.Hook,
		HookDefVersion: req.HookDefVersion,
		ModuleHash:     req.ModuleHash,
		ConfigVersion:  req.ConfigVersion,
		Outcome:        string(res.Outcome),
		Reason:         res.Reason,
		Error:          res.Error,
		Duration:       time.Since(start),
		Exec:           time.Duration(res.ExecNanos),
		ColdStart:      res.ColdStart,
		IdempotencyKey: req.IdempotencyKey,
		Logs:           res.Logs,
		ExecutorID:     e.opts.ExecutorID,
	})
	return res, nil
}

func (e *Executor) execute(ctx context.Context, req execproto.ExecuteRequest) execproto.ExecuteResult {
	var res execproto.ExecuteResult
	if f := e.run(ctx, req, &res); f != nil {
		res.Outcome, res.Reason = f.outcome, f.reason
		res.Result, res.Effects = nil, nil
		if f.err != nil {
			res.Error = truncate(f.err.Error(), e.opts.MaxErrorBytes)
		}
		return res
	}
	res.Outcome = execproto.OutcomeOK
	return res
}

// run fills res for the ok path and returns a failure otherwise. Logs,
// timings and the cold-start flag are filled on both paths.
func (e *Executor) run(ctx context.Context, req execproto.ExecuteRequest, res *execproto.ExecuteResult) *failure {
	view := e.opts.Config.Current()
	if view == nil {
		return fail(execproto.OutcomeUnavailable, execproto.ReasonInternal, errors.New("config not loaded"))
	}
	hook, ok := e.hookDef(view, req.Hook, req.HookDefVersion)
	if !ok {
		return fail(execproto.OutcomeUnavailable, execproto.ReasonStaleConfig,
			fmt.Errorf("hook %s@%d is unknown to this executor", req.Hook, req.HookDefVersion))
	}
	binding, ok := view.Binding(req.TenantID, req.Hook)
	if !ok || binding.ConfigVersion != req.ConfigVersion {
		return fail(execproto.OutcomeUnavailable, execproto.ReasonStaleConfig,
			fmt.Errorf("binding %s/%s with config version %d is unknown to this executor", req.TenantID, req.Hook, req.ConfigVersion))
	}
	limit := config.DefaultConcurrencyLimit
	if tenant, ok := view.Tenant(req.TenantID); ok {
		limit = tenant.ConcurrencyLimit
	}

	mod, f := e.module(ctx, req.ModuleHash, hook)
	if f != nil {
		return f
	}

	key := pool.Key{
		TenantID:       req.TenantID,
		Hook:           req.Hook,
		HookDefVersion: req.HookDefVersion,
		ModuleHash:     req.ModuleHash,
		ConfigVersion:  req.ConfigVersion,
	}
	lease, err := e.opts.Pools.Acquire(ctx, key, limit, func(ctx context.Context) (sandbox.Instance, error) {
		return mod.Instantiate(ctx, binding.Config)
	})
	switch {
	case err != nil && ctx.Err() != nil:
		return notStarted(ctx)
	case errors.Is(err, pool.ErrSaturated):
		return fail(execproto.OutcomeUnavailable, execproto.ReasonPoolSaturated, errors.New("no free instance for this tenant"))
	case err != nil:
		return fail(execproto.OutcomeUnavailable, execproto.ReasonInternal, fmt.Errorf("instantiate: %w", err))
	}
	// A panic below must not leak the pool slot: Release is idempotent, so
	// this only acts when nothing else released the lease.
	defer lease.Release(false)
	res.ColdStart = lease.Cold()
	if ctx.Err() != nil {
		lease.Release(true)
		return notStarted(ctx)
	}

	callStart := time.Now()
	out, err := lease.Instance().Call(ctx, sandbox.Export, req.Payload)
	res.ExecNanos = time.Since(callStart).Nanoseconds()
	res.Logs = formatLogs(out.Logs)
	if err != nil {
		lease.Release(false)
		return callFailure(ctx, err)
	}
	// From here on the instance is healthy whatever the output says.
	lease.Release(true)

	result, effects, err := parseOutput(out.Output, e.opts.MaxOutputBytes, hook.AllowedEffectTypes, req.IdempotencyKey)
	if err != nil {
		var oe *outputError
		errors.As(err, &oe)
		return fail(execproto.OutcomeHandlerError, oe.reason, err)
	}
	outSchema, err := e.opts.Schemas.Get(hook.SchemaKey("output"), hook.OutputSchema)
	if err != nil {
		return fail(execproto.OutcomeUnavailable, execproto.ReasonInternal, fmt.Errorf("output schema: %w", err))
	}
	if err := outSchema.Validate(result); err != nil {
		return fail(execproto.OutcomeHandlerError, execproto.ReasonInvalidOutput, fmt.Errorf("result does not match output schema: %w", err))
	}
	res.Result, res.Effects = result, effects
	return nil
}

// notStarted reports a call whose script never started because the
// caller's context ended first.
func notStarted(ctx context.Context) *failure {
	if errors.Is(ctx.Err(), context.Canceled) {
		return fail(execproto.OutcomeUnavailable, execproto.ReasonCallerCanceled, errors.New("caller went away before the script started"))
	}
	return fail(execproto.OutcomeUnavailable, execproto.ReasonDeadline, errors.New("deadline expired before the script started"))
}

// callFailure maps a script failure. A timeout is attributed to whoever set
// the deadline: the hook's limit (empty reason), the caller's shorter
// deadline, or the caller going away, which is not the tenant's fault.
func callFailure(ctx context.Context, err error) *failure {
	switch {
	case errors.Is(err, sandbox.ErrTimeout) && errors.Is(ctx.Err(), context.Canceled):
		return fail(execproto.OutcomeUnavailable, execproto.ReasonCallerCanceled, errors.New("caller went away while the script ran"))
	case errors.Is(err, sandbox.ErrTimeout) && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fail(execproto.OutcomeTimeout, execproto.ReasonCallerDeadline, nil)
	case errors.Is(err, sandbox.ErrTimeout):
		return fail(execproto.OutcomeTimeout, "", nil)
	case errors.Is(err, sandbox.ErrTrap):
		return fail(execproto.OutcomeHandlerError, execproto.ReasonTrap, err)
	case errors.Is(err, sandbox.ErrGuest):
		return fail(execproto.OutcomeHandlerError, execproto.ReasonGuestError, err)
	default:
		return fail(execproto.OutcomeHandlerError, execproto.ReasonInternal, err)
	}
}

// hookDef returns the pinned hook definition: the current one if versions
// match, otherwise one this executor saw before.
func (e *Executor) hookDef(view *config.View, name string, version int64) (config.HookDef, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if h, ok := view.Hook(name); ok {
		e.hooks[hookKey{h.Name, h.DefVersion}] = h
	}
	h, ok := e.hooks[hookKey{name, version}]
	return h, ok
}

// cached returns a compiled module or a cached invalid verdict for key.
func (e *Executor) cached(key modKey) (sandbox.Module, error, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if m, ok := e.modules[key]; ok {
		return m, nil, true
	}
	if err, ok := e.invalid[key]; ok {
		return nil, err, true
	}
	return nil, nil, false
}

// module returns the module compiled from hash under the hook's spec,
// compiling it at most once concurrently. Compilation is detached from ctx:
// a caller whose deadline expires stops waiting, but the compile finishes
// and is cached for the next call.
func (e *Executor) module(ctx context.Context, hash string, hook config.HookDef) (sandbox.Module, *failure) {
	spec := specOf(hook)
	key := modKey{hash, specDigest(spec)}
	if m, err, ok := e.cached(key); ok {
		if err != nil {
			return nil, fail(execproto.OutcomeHandlerError, execproto.ReasonInvalidModule, err)
		}
		return m, nil
	}

	ch := e.compiles.DoChan(key.hash+"|"+key.spec, func() (any, error) {
		// A flight for this key may have finished between the check above
		// and this one starting.
		if m, err, ok := e.cached(key); ok {
			return m, err
		}
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.opts.CompileTimeout)
		defer cancel()
		return e.compile(cctx, key, spec)
	})
	select {
	case r := <-ch:
		if r.Err != nil {
			return nil, compileFailure(r.Err)
		}
		return r.Val.(sandbox.Module), nil
	case <-ctx.Done():
		return nil, notStarted(ctx)
	}
}

// errFetch marks module store failures, which are retryable.
var errFetch = errors.New("fetch module")

func (e *Executor) compile(ctx context.Context, key modKey, spec sandbox.ModuleSpec) (sandbox.Module, error) {
	wasm, err := e.opts.Modules.Get(ctx, key.hash)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errFetch, err)
	}
	mod, err := e.opts.Runtime.Compile(ctx, wasm, spec)

	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case errors.Is(err, sandbox.ErrInvalidModule):
		e.invalid[key] = err
		return nil, err
	case err != nil:
		return nil, err
	case e.closed:
		mod.Close(ctx)
		return nil, errors.New("executor closed")
	}
	e.modules[key] = mod
	return mod, nil
}

func compileFailure(err error) *failure {
	switch {
	case errors.Is(err, errFetch):
		return fail(execproto.OutcomeUnavailable, execproto.ReasonModuleFetch, err)
	case errors.Is(err, sandbox.ErrInvalidModule):
		return fail(execproto.OutcomeHandlerError, execproto.ReasonInvalidModule, err)
	default:
		return fail(execproto.OutcomeUnavailable, execproto.ReasonInternal, err)
	}
}

// Preload compiles every module bound in the current snapshot, so the first
// calls do not pay for compilation. Compiles run in parallel, one per CPU.
// Failures of individual bindings are joined; the rest still load.
func (e *Executor) Preload(ctx context.Context) error {
	view := e.opts.Config.Current()
	if view == nil {
		return errors.New("preload: config not loaded")
	}
	var (
		mu   sync.Mutex
		errs []error
		g    errgroup.Group
	)
	g.SetLimit(runtime.GOMAXPROCS(0))
	for _, b := range view.Bindings() {
		hook, ok := e.hookDef(view, b.Hook, currentDefVersion(view, b.Hook))
		if !ok {
			errs = append(errs, fmt.Errorf("preload %s/%s: unknown hook", b.TenantID, b.Hook))
			continue
		}
		g.Go(func() error {
			if _, f := e.module(ctx, b.ModuleHash, hook); f != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("preload %s/%s (%s): %w", b.TenantID, b.Hook, b.ModuleHash, f.err))
				mu.Unlock()
			}
			return nil
		})
	}
	g.Wait()
	return errors.Join(errs...)
}

func currentDefVersion(view *config.View, hook string) int64 {
	h, _ := view.Hook(hook)
	return h.DefVersion
}

// Close releases compiled modules. Instances belong to the pool manager,
// which the owner closes first.
func (e *Executor) Close(ctx context.Context) error {
	e.mu.Lock()
	e.closed = true
	mods := e.modules
	e.modules = map[modKey]sandbox.Module{}
	e.mu.Unlock()

	var errs []error
	for _, m := range mods {
		errs = append(errs, m.Close(ctx))
	}
	return errors.Join(errs...)
}

func formatLogs(lines []sandbox.LogLine) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Level + ": " + l.Message
	}
	return out
}
