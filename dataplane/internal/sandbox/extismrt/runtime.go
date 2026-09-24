// Package extismrt implements sandbox.Runtime on the Extism Go SDK (wazero).
//
// Extism specifics this package hides (verified against go-sdk v1.7.1):
//   - every compiled plugin owns a wazero runtime; memory limits are per
//     runtime, so a module is compiled per (bytes, spec);
//   - the manifest timeout must be > 0, otherwise CloseOnContextDone is off
//     and an infinite loop cannot be interrupted;
//   - instances get a reference to the manifest's Config map, so tenant
//     config is assigned as a fresh map, never mutated in place;
//   - the log level is process-global and defaults to Off.
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
	// DisableSharedCompilationCache compiles every module from scratch.
	// Only the compile benchmark uses it.
	DisableSharedCompilationCache bool
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

// Runtime compiles modules with a shared wazero compilation cache, so the
// Extism kernel is compiled once per process instead of once per module.
type Runtime struct {
	opts  Options
	cache wazero.CompilationCache

	mu     sync.Mutex
	mods   map[*module]struct{}
	closed bool
}

var _ sandbox.Runtime = (*Runtime)(nil)

// New creates a runtime and sets Extism's process-global log level.
func New(opts Options) (*Runtime, error) {
	opts.setDefaults()
	extism.SetLogLevel(opts.LogLevel)
	r := &Runtime{opts: opts, mods: make(map[*module]struct{})}
	if !opts.DisableSharedCompilationCache {
		r.cache = wazero.NewCompilationCache()
	}
	return r, nil
}

// Compile checks the module against spec and compiles it.
func (r *Runtime) Compile(ctx context.Context, wasm []byte, spec sandbox.ModuleSpec) (sandbox.Module, error) {
	if spec.Timeout <= 0 || spec.MemoryMaxPages == 0 {
		return nil, fmt.Errorf("%w: timeout and memory limit are required", sandbox.ErrInvalidModule)
	}
	info, err := wasminfo.Inspect(ctx, wasm)
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
	cp, err := extism.NewCompiledPlugin(ctx, manifest, extism.PluginConfig{RuntimeConfig: rc}, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", sandbox.ErrInvalidModule, err)
	}

	m := &module{rt: r, cp: cp, spec: spec}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		cp.Close(ctx)
		return nil, errors.New("extismrt: runtime closed")
	}
	r.mods[m] = struct{}{}
	return m, nil
}

// Close closes every module, then the compilation cache they depend on.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
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

	_, out, err := i.p.CallWithContext(callCtx, export, input)
	logs := i.logs.take()
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
