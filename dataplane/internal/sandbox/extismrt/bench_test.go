package extismrt

import (
	"context"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/sandboxtest"
)

// Spike S1–S4: the cost of each step of the invocation path. Run with
//
//	go test -run '^$' -bench . -benchmem -count 10 ./internal/sandbox/extismrt/

var (
	benchSpec  = sandbox.ModuleSpec{MemoryMaxPages: 256, Timeout: time.Second}
	discountIn = []byte(`{"cart_total":120.5,"customer":{"id":"c1","lifetime_spend":1500}}`)
	discountCf = map[string]string{"threshold": "1000", "percent": "10"}
)

func benchRuntime(b *testing.B, opts Options) *Runtime {
	b.Helper()
	rt, err := New(opts)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { rt.Close(context.Background()) })
	return rt
}

func benchModule(b *testing.B, rt *Runtime, fixture string) sandbox.Module {
	b.Helper()
	m, err := rt.Compile(context.Background(), sandboxtest.Fixture(b, fixture), benchSpec)
	if err != nil {
		b.Fatal(err)
	}
	return m
}

// S1: compile a module. "distinct" compiles new bytes every iteration (a
// real cold miss); "same/sharedCache" recompiles identical bytes with the
// shared compilation cache, which is a full cache hit and measures only
// inspection, decoding and the trial instantiation.
func BenchmarkCompile(b *testing.B) {
	ctx := context.Background()
	wasm := sandboxtest.Fixture(b, "discount")
	cases := []struct {
		name     string
		shared   bool
		distinct bool
	}{
		{"distinct", false, true},
		{"same/noCache", false, false},
		{"same/sharedCache", true, false},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			rt := benchRuntime(b, Options{SharedCompilationCache: tc.shared})
			var n uint64
			b.ReportAllocs()
			for b.Loop() {
				bytes := wasm
				if tc.distinct {
					n++
					bytes = withCustomSection(wasm, n)
				}
				m, err := rt.Compile(ctx, bytes, benchSpec)
				if err != nil {
					b.Fatal(err)
				}
				m.Close(ctx)
			}
		})
	}
}

// S2: create an instance from a compiled module.
func BenchmarkInstantiate(b *testing.B) {
	ctx := context.Background()
	m := benchModule(b, benchRuntime(b, Options{}), "discount")
	b.ReportAllocs()
	for b.Loop() {
		inst, err := m.Instantiate(ctx, discountCf)
		if err != nil {
			b.Fatal(err)
		}
		inst.Close(ctx)
	}
}

// S3: call a warm instance.
func BenchmarkCallWarm(b *testing.B) {
	ctx := context.Background()
	m := benchModule(b, benchRuntime(b, Options{}), "discount")
	inst, err := m.Instantiate(ctx, discountCf)
	if err != nil {
		b.Fatal(err)
	}
	defer inst.Close(ctx)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := inst.Call(ctx, sandbox.Export, discountIn); err != nil {
			b.Fatal(err)
		}
	}
}

// S4: instantiate, call once, close — the cost of a call without a pool.
func BenchmarkCallCold(b *testing.B) {
	ctx := context.Background()
	m := benchModule(b, benchRuntime(b, Options{}), "discount")
	b.ReportAllocs()
	for b.Loop() {
		inst, err := m.Instantiate(ctx, discountCf)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := inst.Call(ctx, sandbox.Export, discountIn); err != nil {
			b.Fatal(err)
		}
		inst.Close(ctx)
	}
}

// S3 in parallel: warm calls on per-goroutine instances, to see how the
// runtime scales across cores.
func BenchmarkCallWarmParallel(b *testing.B) {
	ctx := context.Background()
	m := benchModule(b, benchRuntime(b, Options{}), "discount")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		inst, err := m.Instantiate(ctx, discountCf)
		if err != nil {
			b.Error(err)
			return
		}
		defer inst.Close(ctx)
		for pb.Next() {
			if _, err := inst.Call(ctx, sandbox.Export, discountIn); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
