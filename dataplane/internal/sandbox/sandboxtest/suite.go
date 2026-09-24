package sandboxtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
)

// Spec is the module spec the suite compiles fixtures under.
var Spec = sandbox.ModuleSpec{MemoryMaxPages: 256, Timeout: 100 * time.Millisecond}

// Run checks that a sandbox.Runtime implementation honors the contract:
// outcomes, limits and isolation. newRuntime is called once per subtest.
func Run(t *testing.T, newRuntime func(t *testing.T) sandbox.Runtime) {
	ctx := context.Background()

	compile := func(t *testing.T, rt sandbox.Runtime, name string) sandbox.Module {
		t.Helper()
		m, err := rt.Compile(ctx, Fixture(t, name), Spec)
		if err != nil {
			t.Fatalf("compile %s: %v", name, err)
		}
		t.Cleanup(func() { m.Close(ctx) })
		return m
	}
	instantiate := func(t *testing.T, m sandbox.Module, cfg map[string]string) sandbox.Instance {
		t.Helper()
		i, err := m.Instantiate(ctx, cfg)
		if err != nil {
			t.Fatalf("instantiate: %v", err)
		}
		t.Cleanup(func() { i.Close(ctx) })
		return i
	}
	call := func(t *testing.T, i sandbox.Instance, input string) sandbox.CallResult {
		t.Helper()
		res, err := i.Call(ctx, sandbox.Export, []byte(input))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		return res
	}
	wantOutput := func(t *testing.T, res sandbox.CallResult, fragment string) {
		t.Helper()
		if !bytes.Contains(res.Output, []byte(fragment)) {
			t.Fatalf("output %s lacks %s", res.Output, fragment)
		}
	}

	t.Run("CallOK", func(t *testing.T) {
		i := instantiate(t, compile(t, newRuntime(t), "discount"), map[string]string{"threshold": "500", "percent": "15"})
		res := call(t, i, `{"cart_total":10,"customer":{"id":"c1","lifetime_spend":600}}`)
		var out struct {
			Result struct {
				DiscountPercent int    `json:"discount_percent"`
				Reason          string `json:"reason"`
			} `json:"result"`
		}
		if err := json.Unmarshal(res.Output, &out); err != nil {
			t.Fatalf("output %s: %v", res.Output, err)
		}
		if out.Result.DiscountPercent != 15 || out.Result.Reason != "loyal_customer" {
			t.Fatalf("unexpected output %s", res.Output)
		}
		if len(res.Logs) != 1 || res.Logs[0].Level != "info" || !strings.Contains(res.Logs[0].Message, "discount computed: 15") {
			t.Fatalf("unexpected logs %+v", res.Logs)
		}
	})

	t.Run("LogsAreScopedToCall", func(t *testing.T) {
		i := instantiate(t, compile(t, newRuntime(t), "discount"), nil)
		in := `{"cart_total":10,"customer":{"id":"c1","lifetime_spend":1}}`
		call(t, i, in)
		if res := call(t, i, in); len(res.Logs) != 1 {
			t.Fatalf("second call carries %d log lines, want 1", len(res.Logs))
		}
	})

	t.Run("InstanceReusable", func(t *testing.T) {
		i := instantiate(t, compile(t, newRuntime(t), "counter"), nil)
		for want := 1; want <= 3; want++ {
			wantOutput(t, call(t, i, ""), fmt.Sprintf(`"count":%d`, want))
		}
	})

	t.Run("InstancesIsolated", func(t *testing.T) {
		m := compile(t, newRuntime(t), "counter")
		a, b := instantiate(t, m, nil), instantiate(t, m, nil)
		call(t, a, "")
		call(t, a, "")
		wantOutput(t, call(t, b, ""), `"count":1`)
	})

	t.Run("ConfigIsolated", func(t *testing.T) {
		m := compile(t, newRuntime(t), "echo-config")
		a := instantiate(t, m, map[string]string{"k": "a"})
		b := instantiate(t, m, map[string]string{"k": "b"})
		c := instantiate(t, m, nil)
		in := `{"keys":["k"]}`
		wantOutput(t, call(t, a, in), `"k":"a"`)
		wantOutput(t, call(t, b, in), `"k":"b"`)
		wantOutput(t, call(t, c, in), `"k":null`)
		wantOutput(t, call(t, a, in), `"k":"a"`)
	})

	t.Run("ConfigNotAliased", func(t *testing.T) {
		m := compile(t, newRuntime(t), "echo-config")
		cfg := map[string]string{"k": "before"}
		i := instantiate(t, m, cfg)
		cfg["k"] = "after"
		wantOutput(t, call(t, i, `{"keys":["k"]}`), `"k":"before"`)
	})

	t.Run("Timeout", func(t *testing.T) {
		i := instantiate(t, compile(t, newRuntime(t), "infinite-loop"), nil)
		start := time.Now()
		_, err := i.Call(ctx, sandbox.Export, nil)
		elapsed := time.Since(start)
		if !errors.Is(err, sandbox.ErrTimeout) {
			t.Fatalf("want ErrTimeout, got %v", err)
		}
		if elapsed > Spec.Timeout+250*time.Millisecond {
			t.Fatalf("timeout took %v, limit %v", elapsed, Spec.Timeout)
		}
		if _, err := i.Call(ctx, sandbox.Export, nil); !errors.Is(err, sandbox.ErrClosed) {
			t.Fatalf("instance must be dead after timeout, got %v", err)
		}
	})

	t.Run("CallerDeadlineShorterThanSpec", func(t *testing.T) {
		i := instantiate(t, compile(t, newRuntime(t), "infinite-loop"), nil)
		cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := i.Call(cctx, sandbox.Export, nil)
		if !errors.Is(err, sandbox.ErrTimeout) {
			t.Fatalf("want ErrTimeout, got %v", err)
		}
		if d := time.Since(start); d >= Spec.Timeout {
			t.Fatalf("caller deadline ignored: %v", d)
		}
	})

	t.Run("MemoryLimitTraps", func(t *testing.T) {
		// A small limit and a generous timeout isolate the memory limit from
		// timing: a bomb needs about 1 ms of CPU per MiB of limit (spike S8),
		// so a large limit could hit the timeout first.
		m, err := newRuntime(t).Compile(ctx, Fixture(t, "memory-bomb"), sandbox.ModuleSpec{MemoryMaxPages: 64, Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { m.Close(ctx) })
		i := instantiate(t, m, nil)
		_, err = i.Call(ctx, sandbox.Export, nil)
		if !errors.Is(err, sandbox.ErrTrap) {
			t.Fatalf("want ErrTrap, got %v", err)
		}
	})

	t.Run("GuestError", func(t *testing.T) {
		i := instantiate(t, compile(t, newRuntime(t), "guest-error"), nil)
		_, err := i.Call(ctx, sandbox.Export, nil)
		if !errors.Is(err, sandbox.ErrGuest) || !strings.Contains(err.Error(), "guest failure: boom") {
			t.Fatalf("want ErrGuest with message, got %v", err)
		}
	})

	t.Run("HTTPDenied", func(t *testing.T) {
		rt := newRuntime(t)
		m, err := rt.Compile(ctx, Fixture(t, "http-call"), Spec)
		if err != nil {
			if !errors.Is(err, sandbox.ErrInvalidModule) {
				t.Fatalf("want ErrInvalidModule at compile, got %v", err)
			}
			return // denied statically
		}
		defer m.Close(ctx)
		i := instantiate(t, m, nil)
		if _, err := i.Call(ctx, sandbox.Export, nil); err == nil {
			t.Fatal("http request must fail at runtime")
		}
	})

	t.Run("RejectsGarbage", func(t *testing.T) {
		if _, err := newRuntime(t).Compile(ctx, []byte("nope"), Spec); !errors.Is(err, sandbox.ErrInvalidModule) {
			t.Fatalf("want ErrInvalidModule, got %v", err)
		}
	})

	t.Run("RequiresLimits", func(t *testing.T) {
		rt := newRuntime(t)
		for _, spec := range []sandbox.ModuleSpec{
			{MemoryMaxPages: 256},
			{Timeout: time.Second},
		} {
			if _, err := rt.Compile(ctx, Fixture(t, "discount"), spec); !errors.Is(err, sandbox.ErrInvalidModule) {
				t.Fatalf("spec %+v: want ErrInvalidModule, got %v", spec, err)
			}
		}
	})

	t.Run("RejectsModulesThatCannotLink", func(t *testing.T) {
		kernel := "extism:host/env"
		cases := map[string]TestModule{
			"memory import": {HandleResults: []byte{I32}, MemoryImport: &FuncImport{Module: "env", Name: "memory"}},
			"kernel import with wrong signature": {HandleResults: []byte{I32},
				FuncImports: []FuncImport{{Module: kernel, Name: "alloc", Params: []byte{I32}, Results: []byte{I32}}}},
			"handle with params":    {HandleParams: []byte{I32}, HandleResults: []byte{I32}},
			"memory over the limit": {HandleResults: []byte{I32}, MemoryMin: Spec.MemoryMaxPages + 1},
		}
		rt := newRuntime(t)
		for name, mod := range cases {
			if _, err := rt.Compile(ctx, mod.Build(), Spec); !errors.Is(err, sandbox.ErrInvalidModule) {
				t.Errorf("%s: want ErrInvalidModule at compile, got %v", name, err)
			}
		}
	})

	t.Run("NonZeroExitIsGuestError", func(t *testing.T) {
		rt := newRuntime(t)
		m, err := rt.Compile(ctx, TestModule{HandleResults: []byte{I32}, ReturnCode: 1}.Build(), Spec)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { m.Close(ctx) })
		if _, err := instantiate(t, m, nil).Call(ctx, sandbox.Export, nil); !errors.Is(err, sandbox.ErrGuest) {
			t.Fatalf("want ErrGuest for exit code 1 without a message, got %v", err)
		}
	})

	t.Run("CompileAfterCloseFails", func(t *testing.T) {
		rt := newRuntime(t)
		if err := rt.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := rt.Compile(ctx, Fixture(t, "counter"), Spec); err == nil {
			t.Fatal("compile after Close must fail")
		}
	})

	t.Run("ConcurrentInstances", func(t *testing.T) {
		m := compile(t, newRuntime(t), "echo-config")
		var wg sync.WaitGroup
		errs := make(chan error, 16)
		for n := range 16 {
			wg.Go(func() {
				v := fmt.Sprintf("v%d", n)
				i, err := m.Instantiate(ctx, map[string]string{"k": v})
				if err != nil {
					errs <- err
					return
				}
				defer i.Close(ctx)
				for range 20 {
					res, err := i.Call(ctx, sandbox.Export, []byte(`{"keys":["k"]}`))
					if err != nil {
						errs <- err
						return
					}
					if !bytes.Contains(res.Output, []byte(`"k":"`+v+`"`)) {
						errs <- fmt.Errorf("instance %d saw %s", n, res.Output)
						return
					}
				}
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	})
}
