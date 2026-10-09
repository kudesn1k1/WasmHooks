package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
)

// ValidateRequest asks whether a module fits a hook definition
// (POST /internal/v1/modules/validate).
type ValidateRequest struct {
	ModuleHash string
	Hook       config.HookDef
	Config     map[string]string // tenant config for the sample call; nil means empty
}

// ValidationCheck is one line of a ValidationReport.
type ValidationCheck struct {
	Name   string `json:"name"` // fetch, compile, exports, imports, sample_call
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// ValidationReport is the verdict on a module, in the shape of
// api/dataplane-internal.openapi.yaml.
type ValidationReport struct {
	OK     bool              `json:"ok"`
	Checks []ValidationCheck `json:"checks"`
}

// ErrUnavailable: the module cannot be judged right now (module storage
// outage, a platform failure while compiling, the caller gave up while
// waiting). The caller should retry later; it says nothing about the module.
var ErrUnavailable = errors.New("executor: validation unavailable")

// ErrInvalidHook: the hook definition itself cannot be used (a schema the
// data plane cannot compile). It is the request's fault, not the module's:
// the internal API answers 400.
var ErrInvalidHook = errors.New("executor: invalid hook definition")

var checkOrder = []string{"fetch", "compile", "exports", "imports", "sample_call"}

// reportBuilder fills the checks in contract order.
type reportBuilder struct{ checks []ValidationCheck }

func (b *reportBuilder) pass(name string) *reportBuilder {
	b.checks = append(b.checks, ValidationCheck{Name: name, OK: true})
	return b
}

// fail records the failed check; every later check is reported as skipped.
func (b *reportBuilder) fail(name, detail string) ValidationReport {
	b.checks = append(b.checks, ValidationCheck{Name: name, OK: false, Detail: detail})
	for _, rest := range checkOrder[len(b.checks):] {
		b.checks = append(b.checks, ValidationCheck{Name: rest, OK: false, Detail: "skipped"})
	}
	return ValidationReport{OK: false, Checks: b.checks}
}

func (b *reportBuilder) done() ValidationReport {
	return ValidationReport{OK: true, Checks: b.checks}
}

// Validate checks a module against a hook definition: it fetches the module,
// compiles it under the hook's limits, checks exports and imports, and runs
// it once on the hook's sample input. It touches neither the module cache
// nor the instance pools: a rejected module must not occupy them, and the
// hook definition may not be published yet. At most ValidateConcurrency
// validations run at once, so a wave of uploads cannot take the CPU from
// live calls.
func (e *Executor) Validate(ctx context.Context, req ValidateRequest) (ValidationReport, error) {
	select {
	case e.validateSem <- struct{}{}:
		defer func() { <-e.validateSem }()
	case <-ctx.Done():
		return ValidationReport{}, fmt.Errorf("%w: waiting for a validation slot: %w", ErrUnavailable, ctx.Err())
	}
	// Schemas first: a schema the data plane cannot compile would otherwise
	// be reported as the module's failure. Compiled here, not through the
	// shared schema cache: the definition may not be published yet.
	if _, err := schema.Compile(req.Hook.InputSchema); err != nil {
		return ValidationReport{}, fmt.Errorf("%w: input_schema: %w", ErrInvalidHook, err)
	}
	outSchema, err := schema.Compile(req.Hook.OutputSchema)
	if err != nil {
		return ValidationReport{}, fmt.Errorf("%w: output_schema: %w", ErrInvalidHook, err)
	}
	b := &reportBuilder{}

	wasm, err := e.opts.Modules.Get(ctx, req.ModuleHash)
	switch {
	case errors.Is(err, modstore.ErrNotFound), errors.Is(err, modstore.ErrHashMismatch),
		errors.Is(err, modstore.ErrBadHash), errors.Is(err, modstore.ErrTooLarge):
		return b.fail("fetch", err.Error()), nil
	case err != nil:
		return ValidationReport{}, fmt.Errorf("%w: fetch: %w", ErrUnavailable, err)
	}
	b.pass("fetch")

	cctx, cancel := context.WithTimeout(ctx, e.opts.CompileTimeout)
	defer cancel()
	mod, err := e.opts.Runtime.Compile(cctx, wasm, specOf(req.Hook)) // not e.module(): the cache is for bound modules
	var me *sandbox.ModuleError
	switch {
	case errors.As(err, &me) && len(me.Export) > 0:
		b.pass("compile")
		return b.fail("exports", strings.Join(me.Export, "; ")), nil
	case errors.As(err, &me):
		b.pass("compile").pass("exports")
		return b.fail("imports", strings.Join(me.Imports, "; ")), nil
	case errors.Is(err, sandbox.ErrInvalidModule):
		return b.fail("compile", err.Error()), nil
	case err != nil:
		return ValidationReport{}, fmt.Errorf("%w: compile: %w", ErrUnavailable, err)
	}
	defer mod.Close(context.WithoutCancel(ctx))
	b.pass("compile").pass("exports").pass("imports")

	detail, err := e.sampleCall(ctx, mod, req, outSchema)
	if err != nil {
		return ValidationReport{}, err
	}
	if detail != "" {
		return b.fail("sample_call", detail), nil
	}
	return b.pass("sample_call").done(), nil
}

// sampleCall runs the module on the hook's sample input in a throwaway
// instance and checks the output against outSchema. It returns why the call
// failed the hook contract, or "" if it passed; an error means the platform,
// not the module, failed.
//
// A timeout is retried once on a fresh instance: under CPU load the first
// call of a new instance can miss timeout_ms through no fault of the module,
// and the verdict is final (the control plane marks the module rejected).
func (e *Executor) sampleCall(ctx context.Context, mod sandbox.Module, req ValidateRequest, outSchema *schema.Schema) (string, error) {
	cfg := req.Config
	if cfg == nil {
		cfg = map[string]string{}
	}
	var out sandbox.CallResult
	for attempt := 1; ; attempt++ {
		var timedOut bool
		var detail string
		var err error
		out, timedOut, detail, err = e.callOnce(ctx, mod, cfg, req)
		if err != nil || detail != "" {
			return detail, err
		}
		if !timedOut {
			break
		}
		if attempt == 2 {
			return fmt.Sprintf("timeout after %d ms (twice)", req.Hook.TimeoutMS), nil
		}
	}

	result, _, err := parseOutput(out.Output, e.opts.MaxOutputBytes, req.Hook.AllowedEffectTypes, "validate")
	if err != nil {
		return "handler_error: " + err.Error(), nil
	}
	if err := outSchema.Validate(result); err != nil {
		return "handler_error: result does not match output_schema: " + err.Error(), nil
	}
	return "", nil
}

// callOnce makes one sample call. timedOut reports a timeout by the hook's
// own limit; detail is a module failure other than a timeout.
func (e *Executor) callOnce(ctx context.Context, mod sandbox.Module, cfg map[string]string, req ValidateRequest) (out sandbox.CallResult, timedOut bool, detail string, err error) {
	inst, err := mod.Instantiate(ctx, cfg)
	if err != nil {
		if ctx.Err() != nil {
			return out, false, "", fmt.Errorf("%w: instantiate: %w", ErrUnavailable, err)
		}
		return out, false, "instantiate: " + err.Error(), nil
	}
	defer inst.Close(context.WithoutCancel(ctx))

	callCtx, cancel := context.WithTimeout(ctx, req.Hook.Timeout())
	defer cancel()
	out, err = inst.Call(callCtx, sandbox.Export, req.Hook.SampleInput)
	switch {
	case errors.Is(err, sandbox.ErrTimeout) && ctx.Err() != nil:
		return out, false, "", fmt.Errorf("%w: the caller gave up during the sample call: %w", ErrUnavailable, ctx.Err())
	case errors.Is(err, sandbox.ErrTimeout):
		return out, true, "", nil
	case err != nil:
		return out, false, "handler_error: " + err.Error(), nil
	}
	return out, false, "", nil
}
