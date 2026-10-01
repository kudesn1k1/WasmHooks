// Package extismrt implements sandbox.Runtime on the Extism Go SDK (wazero).
//
// Extism specifics this package hides (verified against go-sdk v1.7.1):
//   - every compiled plugin owns a wazero runtime; memory limits are per
//     runtime, so a module is compiled per (bytes, spec);
//   - the manifest timeout must be > 0, otherwise CloseOnContextDone is off
//     and an infinite loop cannot be interrupted;
//   - instances get a reference to the manifest's Config map, so tenant
//     config is assigned as a fresh map, never mutated in place;
//   - the log level is process-global and defaults to Off;
//   - a non-zero return code without an error message is still a failure;
//   - with a shared wazero CompilationCache, closing a compiled plugin does
//     not free its machine code, so the cache is opt-in.
package extismrt

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	extism "github.com/extism/go-sdk"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/sys"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/wasminfo"
)

// Options tune the runtime. Zero values take the defaults.
type Options struct {
	LogLevel    extism.LogLevel // minimum guest log level captured; default Info
	MaxVarBytes int64           // cap on Extism vars per instance; default 64 KiB
	MaxLogBytes int             // per call; default 16 KiB
	MaxLogLines int             // per call; default 100
	// SharedCompilationCache reuses machine code for identical module bytes
	// across compiles. Closing a module then no longer frees its code (it
	// lives until Runtime.Close), so it is off by default; the compile
	// benchmark uses it.
	SharedCompilationCache bool
}

func (o *Options) setDefaults() {
	if o.LogLevel == 0 {
		o.LogLevel = extism.LogLevelInfo
	}
	if o.MaxVarBytes <= 0 {
		o.MaxVarBytes = 64 << 10
	}
	if o.MaxLogBytes <= 0 {
		o.MaxLogBytes = 16 << 10
	}
	if o.MaxLogLines <= 0 {
		o.MaxLogLines = 100
	}
}

// Runtime compiles modules. Compiles hold a read lock for their whole
// duration and Close takes the write lock, so nothing compiles against a
// closed runtime.
type Runtime struct {
	opts  Options
	cache wazero.CompilationCache // nil unless SharedCompilationCache

	lifecycle sync.RWMutex // held for reading by compiles, for writing by Close
	closed    bool         // guarded by lifecycle

	mu   sync.Mutex
	mods map[*module]struct{}
}

var _ sandbox.Runtime = (*Runtime)(nil)

// New creates a runtime and sets Extism's process-global log level.
func New(opts Options) (*Runtime, error) {
	opts.setDefaults()
	extism.SetLogLevel(opts.LogLevel)
	r := &Runtime{opts: opts, mods: make(map[*module]struct{})}
	if opts.SharedCompilationCache {
		r.cache = wazero.NewCompilationCache()
	}
	return r, nil
}

// errClosed is returned by Compile after Close.
var errClosed = errors.New("extismrt: runtime closed")

// Compile checks the module against spec, compiles it and instantiates it
// once to prove it links. Everything the module can get wrong is reported as
// sandbox.ErrInvalidModule; other errors are the platform's.
func (r *Runtime) Compile(ctx context.Context, wasm []byte, spec sandbox.ModuleSpec) (sandbox.Module, error) {
	if spec.Timeout <= 0 || spec.MemoryMaxPages == 0 {
		return nil, fmt.Errorf("%w: timeout and memory limit are required", sandbox.ErrInvalidModule)
	}
	r.lifecycle.RLock()
	defer r.lifecycle.RUnlock()
	if r.closed {
		return nil, errClosed
	}

	info, err := wasminfo.Inspect(ctx, wasm, spec.MemoryMaxPages)
	if err != nil {
		return nil, err
	}
	if err := sandbox.CheckModule(info, spec); err != nil {
		return nil, err
	}

	rc := wazero.NewRuntimeConfig()
	if r.cache != nil {
		rc = rc.WithCompilationCache(r.cache)
	}
	timeoutMS := uint64(spec.Timeout.Milliseconds())
	if timeoutMS == 0 {
		timeoutMS = 1
	}
	manifest := extism.Manifest{
		Wasm:    []extism.Wasm{extism.WasmData{Data: wasm}},
		Memory:  &extism.ManifestMemory{MaxPages: spec.MemoryMaxPages, MaxVarBytes: r.opts.MaxVarBytes},
		Timeout: timeoutMS,
		// AllowedHosts stays empty: the SDK rejects every HTTP request.
	}
	// The module was decoded and checked above, so a failure here is the
	// platform's (executable memory refused, out of memory, a kernel that
	// does not fit the limit), not the tenant's.
	cp, err := extism.NewCompiledPlugin(ctx, manifest, extism.PluginConfig{RuntimeConfig: rc}, nil)
	if err != nil {
		return nil, fmt.Errorf("extismrt: compile: %w", err)
	}
	// Linking happens at instantiation: imports with wrong signatures and
	// imported tables or globals only fail there, and instantiation runs the
	// module's start section. Do it once, here, within the hook's time limit:
	// a module that cannot start in the time a call may take is invalid.
	trialCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	trial, err := cp.Instance(trialCtx, extism.PluginInstanceConfig{})
	cancel()
	if err != nil {
		cp.Close(ctx)
		if strings.Contains(err.Error(), "instantiating extism module") {
			// The Extism kernel is ours: failing to instantiate it is a
			// platform problem, not the tenant's.
			return nil, fmt.Errorf("extismrt: instantiate kernel: %w", err)
		}
		return nil, fmt.Errorf("%w: %v", sandbox.ErrInvalidModule, err)
	}
	trial.Close(ctx)

	m := &module{rt: r, cp: cp, spec: spec}
	r.mu.Lock()
	r.mods[m] = struct{}{}
	r.mu.Unlock()
	return m, nil
}

// Close waits for running compiles, then closes every module and the
// compilation cache they depend on.
func (r *Runtime) Close(ctx context.Context) error {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	r.mu.Lock()
	mods := r.mods
	r.mods = nil
	r.mu.Unlock()

	var errs []error
	for m := range mods {
		errs = append(errs, m.cp.Close(ctx))
	}
	if r.cache != nil {
		errs = append(errs, r.cache.Close(ctx))
	}
	return errors.Join(errs...)
}

type module struct {
	rt   *Runtime
	cp   *extism.CompiledPlugin
	spec sandbox.ModuleSpec
	once sync.Once
}

func (m *module) Instantiate(ctx context.Context, cfg map[string]string) (sandbox.Instance, error) {
	p, err := m.cp.Instance(ctx, extism.PluginInstanceConfig{})
	if err != nil {
		return nil, fmt.Errorf("extismrt: instantiate: %w", err)
	}
	// Never mutate p.Config: it is the manifest's map, shared by all instances.
	p.Config = maps.Clone(cfg)
	if p.Config == nil {
		p.Config = map[string]string{}
	}
	inst := &instance{
		p:       p,
		timeout: m.spec.Timeout,
		logs:    logBuffer{maxBytes: m.rt.opts.MaxLogBytes, maxLines: m.rt.opts.MaxLogLines},
	}
	p.SetLogger(inst.logs.add)
	return inst, nil
}

func (m *module) Close(ctx context.Context) error {
	var err error
	m.once.Do(func() {
		m.rt.mu.Lock()
		delete(m.rt.mods, m)
		m.rt.mu.Unlock()
		err = m.cp.Close(ctx)
	})
	return err
}

// instance is used by one caller at a time (sandbox.Instance contract), so
// its fields need no locking.
type instance struct {
	p       *extism.Plugin
	timeout time.Duration
	logs    logBuffer
	dead    bool
}

func (i *instance) Call(ctx context.Context, export string, input []byte) (sandbox.CallResult, error) {
	if i.dead {
		return sandbox.CallResult{}, sandbox.ErrClosed
	}
	i.logs.reset()
	callCtx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	rc, out, err := i.p.CallWithContext(callCtx, export, input)
	logs := i.logs.take()
	if err == nil && rc != 0 {
		err = fmt.Errorf("exit code %d without an error message", rc)
	}
	if err != nil {
		i.dead = true
		return sandbox.CallResult{Logs: logs}, classify(callCtx, err)
	}
	return sandbox.CallResult{Output: out, Logs: logs}, nil
}

func (i *instance) Close(ctx context.Context) error {
	i.dead = true
	return i.p.Close(ctx)
}

// classify maps an Extism call error to a sandbox error.
func classify(callCtx context.Context, err error) error {
	var exit *sys.ExitError
	isExit := errors.As(err, &exit)
	switch {
	case isExit && (exit.ExitCode() == sys.ExitCodeDeadlineExceeded || exit.ExitCode() == sys.ExitCodeContextCanceled):
		return fmt.Errorf("%w: %v", sandbox.ErrTimeout, err)
	case callCtx.Err() != nil:
		return fmt.Errorf("%w: %v", sandbox.ErrTimeout, err)
	case strings.Contains(err.Error(), "module is closed"):
		return fmt.Errorf("%w: %v", sandbox.ErrClosed, err)
	case isExit || strings.HasPrefix(err.Error(), "wasm error:"):
		return fmt.Errorf("%w: %v", sandbox.ErrTrap, err)
	default:
		return fmt.Errorf("%w: %v", sandbox.ErrGuest, err)
	}
}
