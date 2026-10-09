package executor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/pool"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/sandboxtest"
)

// emptyModule is the smallest valid wasm module: magic and version, nothing
// else. It compiles but exports no handle.
var emptyModule = []byte("\x00asm\x01\x00\x00\x00")

func validateHook(out json.RawMessage) config.HookDef {
	return config.HookDef{
		Name: hookName, DefVersion: 7,
		InputSchema: inSchema, OutputSchema: out,
		// Generous: a real sample call under -race on a busy CI runner. Only
		// the infinite-loop case uses a short limit (review 2, I1).
		TimeoutMS: 2000, MemoryMaxPages: 64,
		SampleInput: discountIn,
	}
}

// put stores wasm in the env's module directory and returns its hash.
func (e *env) put(t *testing.T, wasm []byte) string {
	t.Helper()
	hash := modstore.HashOf(wasm)
	if err := os.WriteFile(filepath.Join(e.dir, strings.TrimPrefix(hash, "sha256:")+".wasm"), wasm, 0o644); err != nil {
		t.Fatal(err)
	}
	return hash
}

func checks(r ValidationReport) string {
	var parts []string
	for _, c := range r.Checks {
		state := "ok"
		if !c.OK {
			state = "FAIL"
			if c.Detail == "skipped" {
				state = "skip"
			}
		}
		parts = append(parts, c.Name+"="+state)
	}
	return strings.Join(parts, " ")
}

func TestValidate(t *testing.T) {
	e := newEnv(t, nil, anyOut, pool.Options{})
	missing := "sha256:" + strings.Repeat("0", 64)
	tests := []struct {
		name       string
		wasm       []byte // nil: use hash as is
		hash       string
		out        json.RawMessage
		config     map[string]string
		sample     json.RawMessage
		timeoutMS  int64
		wantOK     bool
		wantChecks string
		detail     string // substring of the failed check's detail
	}{
		{name: "discount passes", wasm: sandboxtest.Fixture(t, "discount"), out: discountOut, wantOK: true,
			wantChecks: "fetch=ok compile=ok exports=ok imports=ok sample_call=ok"},
		{name: "module not in storage", hash: missing,
			wantChecks: "fetch=FAIL compile=skip exports=skip imports=skip sample_call=skip", detail: "not found"},
		{name: "forbidden http import", wasm: sandboxtest.Fixture(t, "http-call"),
			wantChecks: "fetch=ok compile=ok exports=ok imports=FAIL sample_call=skip", detail: "http_request"},
		{name: "not wasm", wasm: []byte("definitely not wasm"),
			wantChecks: "fetch=ok compile=FAIL exports=skip imports=skip sample_call=skip"},
		{name: "no handle export", wasm: emptyModule,
			wantChecks: "fetch=ok compile=ok exports=FAIL imports=skip sample_call=skip", detail: `missing export "handle"`},
		{name: "infinite loop", wasm: sandboxtest.Fixture(t, "infinite-loop"), timeoutMS: 50,
			wantChecks: "fetch=ok compile=ok exports=ok imports=ok sample_call=FAIL", detail: "timeout after 50 ms (twice)"},
		{name: "output breaks the contract", wasm: sandboxtest.Fixture(t, "bad-output"), out: discountOut,
			wantChecks: "fetch=ok compile=ok exports=ok imports=ok sample_call=FAIL", detail: "handler_error"},
		{name: "config reaches the sample call", wasm: sandboxtest.Fixture(t, "echo-config"),
			out:    json.RawMessage(`{"type":"object","required":["config"],"properties":{"config":{"type":"object","properties":{"k":{"const":"v"}},"required":["k"]}}}`),
			config: map[string]string{"k": "v"}, sample: json.RawMessage(`{"keys":["k"]}`), wantOK: true,
			wantChecks: "fetch=ok compile=ok exports=ok imports=ok sample_call=ok"},
		{name: "without config the same script fails the schema", wasm: sandboxtest.Fixture(t, "echo-config"),
			out:        json.RawMessage(`{"type":"object","required":["config"],"properties":{"config":{"type":"object","properties":{"k":{"const":"v"}},"required":["k"]}}}`),
			sample:     json.RawMessage(`{"keys":["k"]}`),
			wantChecks: "fetch=ok compile=ok exports=ok imports=ok sample_call=FAIL", detail: "output_schema"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := tt.hash
			if tt.wasm != nil {
				hash = e.put(t, tt.wasm)
			}
			out := tt.out
			if out == nil {
				out = anyOut
			}
			hook := validateHook(out)
			if tt.sample != nil {
				hook.SampleInput = tt.sample
			}
			if tt.timeoutMS != 0 {
				hook.TimeoutMS = tt.timeoutMS
			}
			report, err := e.exec.Validate(context.Background(), ValidateRequest{ModuleHash: hash, Hook: hook, Config: tt.config})
			if err != nil {
				t.Fatalf("Validate error: %v", err)
			}
			if report.OK != tt.wantOK || checks(report) != tt.wantChecks {
				t.Fatalf("report = ok:%v %s\nwant  = ok:%v %s\n%+v", report.OK, checks(report), tt.wantOK, tt.wantChecks, report.Checks)
			}
			if tt.detail != "" {
				var failed ValidationCheck
				for _, c := range report.Checks {
					if !c.OK && c.Detail != "skipped" {
						failed = c
					}
				}
				if !strings.Contains(failed.Detail, tt.detail) {
					t.Fatalf("failed check %q detail %q does not mention %q", failed.Name, failed.Detail, tt.detail)
				}
			}
		})
	}
}

// brokenStore fails every Get the way a storage outage does.
type brokenStore struct{}

func (brokenStore) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("connection refused")
}

func TestValidateStorageOutageIsUnavailable(t *testing.T) {
	e := newEnv(t, nil, anyOut, pool.Options{})
	exec := New(Options{Runtime: e.rt, Modules: brokenStore{}, Config: e.cfg, Pools: e.pools, Schemas: nil, Sink: e.sink})
	_, err := exec.Validate(context.Background(), ValidateRequest{
		ModuleHash: modstore.HashOf(emptyModule), Hook: validateHook(anyOut),
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestValidateDoesNotCacheModules(t *testing.T) {
	e := newEnv(t, nil, anyOut, pool.Options{})
	hash := e.put(t, sandboxtest.Fixture(t, "discount"))
	for range 2 {
		report, err := e.exec.Validate(context.Background(), ValidateRequest{ModuleHash: hash, Hook: validateHook(discountOut)})
		if err != nil || !report.OK {
			t.Fatalf("Validate = %+v, %v", report, err)
		}
	}
	if got := e.rt.compiles.Load(); got != 2 {
		t.Fatalf("compiles = %d, want 2: Validate must not reuse or fill the module cache", got)
	}
	e.exec.mu.Lock()
	cached := len(e.exec.modules)
	e.exec.mu.Unlock()
	if cached != 0 {
		t.Fatalf("module cache has %d entries after Validate, want 0", cached)
	}
}

// slowRuntime records how many compiles run at once.
type slowRuntime struct {
	sandbox.Runtime
	running, peak atomic.Int32
}

func (s *slowRuntime) Compile(ctx context.Context, wasm []byte, spec sandbox.ModuleSpec) (sandbox.Module, error) {
	n := s.running.Add(1)
	defer s.running.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(30 * time.Millisecond)
	return s.Runtime.Compile(ctx, wasm, spec)
}

func TestValidateConcurrencyIsCapped(t *testing.T) {
	e := newEnv(t, nil, anyOut, pool.Options{})
	hash := e.put(t, sandboxtest.Fixture(t, "discount"))
	slow := &slowRuntime{Runtime: e.rt}
	exec := New(Options{Runtime: slow, Modules: modstore.NewFS(e.dir), Config: e.cfg, Pools: e.pools, Sink: e.sink, ValidateConcurrency: 1})

	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := exec.Validate(context.Background(), ValidateRequest{ModuleHash: hash, Hook: validateHook(discountOut)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak := slow.peak.Load(); peak != 1 {
		t.Fatalf("peak concurrent validations = %d, want 1", peak)
	}
}

func TestValidateGivesUpWaitingForASlot(t *testing.T) {
	e := newEnv(t, nil, anyOut, pool.Options{})
	e.exec.validateSem <- struct{}{} // the only slot is busy
	defer func() { <-e.exec.validateSem }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := e.exec.Validate(ctx, ValidateRequest{ModuleHash: modstore.HashOf(emptyModule), Hook: validateHook(anyOut)})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// flakyModule times out on the first call and succeeds afterwards, like a
// module whose first call landed on a busy CPU.
type flakyModule struct{ calls atomic.Int32 }

func (m *flakyModule) Instantiate(context.Context, map[string]string) (sandbox.Instance, error) {
	return flakyInstance{m}, nil
}
func (m *flakyModule) Close(context.Context) error { return nil }

type flakyInstance struct{ m *flakyModule }

func (i flakyInstance) Call(context.Context, string, []byte) (sandbox.CallResult, error) {
	if i.m.calls.Add(1) == 1 {
		return sandbox.CallResult{}, sandbox.ErrTimeout
	}
	return sandbox.CallResult{Output: []byte(`{"result":{"discount_percent":5}}`)}, nil
}
func (flakyInstance) Close(context.Context) error { return nil }

type flakyRuntime struct{ mod *flakyModule }

func (r flakyRuntime) Compile(context.Context, []byte, sandbox.ModuleSpec) (sandbox.Module, error) {
	return r.mod, nil
}
func (flakyRuntime) Close(context.Context) error { return nil }

func TestValidateRetriesATimedOutSampleCallOnce(t *testing.T) {
	e := newEnv(t, nil, anyOut, pool.Options{})
	hash := e.put(t, emptyModule)
	mod := &flakyModule{}
	exec := New(Options{Runtime: flakyRuntime{mod}, Modules: modstore.NewFS(e.dir), Config: e.cfg, Pools: e.pools, Sink: e.sink})
	report, err := exec.Validate(context.Background(), ValidateRequest{ModuleHash: hash, Hook: validateHook(discountOut)})
	if err != nil || !report.OK {
		t.Fatalf("Validate = %s, %v; a single timeout must be retried", checks(report), err)
	}
	if got := mod.calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

func TestValidateRejectsUncompilableSchemasAsTheHooksFault(t *testing.T) {
	// Review 2, I3: a schema the data plane cannot compile is the request's
	// problem (400), never a module verdict.
	e := newEnv(t, nil, anyOut, pool.Options{})
	hash := e.put(t, sandboxtest.Fixture(t, "discount"))
	for name, hook := range map[string]config.HookDef{
		"lookahead in output": validateHook(json.RawMessage(`{"type":"object","properties":{"a":{"type":"string","pattern":"^(?=a)"}}}`)),
		"dangling ref":        validateHook(json.RawMessage(`{"type":"object","properties":{"a":{"$ref":"#/$defs/nope"}}}`)),
		"foreign dialect in input": func() config.HookDef {
			h := validateHook(anyOut)
			h.InputSchema = json.RawMessage(`{"$schema":"https://example.com/my-meta","type":"object"}`)
			return h
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.exec.Validate(context.Background(), ValidateRequest{ModuleHash: hash, Hook: hook})
			if !errors.Is(err, ErrInvalidHook) {
				t.Fatalf("err = %v, want ErrInvalidHook", err)
			}
		})
	}
}
