package extismrt

import (
	"context"
	"encoding/binary"
	"flag"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/sandboxtest"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/wasminfo"
)

// Spike measurements S5–S8, S11. They print tables instead of asserting and
// run only with -spike:
//
//	go test -run 'Spike' -spike -v ./internal/sandbox/extismrt/

var spike = flag.Bool("spike", false, "run spike measurements")

func requireSpike(t *testing.T) {
	if !*spike {
		t.Skip("spike measurement; run with -spike")
	}
}

func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapInuse
}

func mib(b uint64) float64 { return float64(b) / (1 << 20) }

// S5: Go heap per live instance, before and after its first call (the first
// call lets the guest allocate its working set).
func TestSpikeInstanceMemory(t *testing.T) {
	requireSpike(t)
	ctx := context.Background()
	rt, _ := New(Options{})
	defer rt.Close(ctx)
	m, err := rt.Compile(ctx, sandboxtest.Fixture(t, "discount"), benchSpec)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 10, 100, 500} {
		base := heapInUse()
		insts := make([]sandbox.Instance, 0, n)
		for range n {
			inst, err := m.Instantiate(ctx, discountCf)
			if err != nil {
				t.Fatal(err)
			}
			insts = append(insts, inst)
		}
		idle := heapInUse()
		for _, inst := range insts {
			if _, err := inst.Call(ctx, sandbox.Export, discountIn); err != nil {
				t.Fatal(err)
			}
		}
		used := heapInUse()
		t.Logf("S5 instances=%4d  heap/instance idle=%.2f MiB  after first call=%.2f MiB",
			n, mib(idle-base)/float64(n), mib(used-base)/float64(n))
		for _, inst := range insts {
			inst.Close(ctx)
		}
	}
}

// withCustomSection returns wasm with a custom section appended, so the
// bytes (and the content hash) differ while the code stays the same.
func withCustomSection(wasm []byte, id uint64) []byte {
	name := "spike"
	var payload []byte
	payload = binary.AppendUvarint(payload, uint64(len(name)))
	payload = append(payload, name...)
	payload = binary.LittleEndian.AppendUint64(payload, id)
	out := slices.Clone(wasm)
	out = append(out, 0) // custom section id
	out = binary.AppendUvarint(out, uint64(len(payload)))
	return append(out, payload...)
}

func percentile(ds []time.Duration, p float64) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[int(float64(len(s)-1)*p)]
}

// S6: memory and compile time per distinct compiled module, and warm call
// latency while K modules are loaded.
func TestSpikeModuleScaling(t *testing.T) {
	requireSpike(t)
	ctx := context.Background()
	wasm := sandboxtest.Fixture(t, "discount")
	for _, k := range []int{1, 10, 50, 100} {
		rt, _ := New(Options{})
		base := heapInUse()
		start := time.Now()
		mods := make([]sandbox.Module, 0, k)
		for i := range k {
			m, err := rt.Compile(ctx, withCustomSection(wasm, uint64(i)), benchSpec)
			if err != nil {
				t.Fatal(err)
			}
			mods = append(mods, m)
		}
		compileAvg := time.Since(start) / time.Duration(k)
		heap := heapInUse() - base

		inst, err := mods[0].Instantiate(ctx, discountCf)
		if err != nil {
			t.Fatal(err)
		}
		lat := make([]time.Duration, 2000)
		for i := range lat {
			s := time.Now()
			if _, err := inst.Call(ctx, sandbox.Export, discountIn); err != nil {
				t.Fatal(err)
			}
			lat[i] = time.Since(s)
		}
		inst.Close(ctx)
		t.Logf("S6 modules=%3d  compile avg=%v  heap/module=%.2f MiB  warm call p50=%v p99=%v",
			k, compileAvg.Round(time.Microsecond), mib(heap)/float64(k),
			percentile(lat, 0.5).Round(time.Microsecond), percentile(lat, 0.99).Round(time.Microsecond))
		rt.Close(ctx)
	}
}

// S7: how far past its deadline an infinite loop is interrupted, and
// whether the instance is dead afterwards.
func TestSpikeTimeoutPrecision(t *testing.T) {
	requireSpike(t)
	ctx := context.Background()
	rt, _ := New(Options{})
	defer rt.Close(ctx)
	for _, limit := range []time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond} {
		m, err := rt.Compile(ctx, sandboxtest.Fixture(t, "infinite-loop"), sandbox.ModuleSpec{MemoryMaxPages: 64, Timeout: limit})
		if err != nil {
			t.Fatal(err)
		}
		over := make([]time.Duration, 30)
		dead := 0
		for i := range over {
			inst, err := m.Instantiate(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			s := time.Now()
			_, err = inst.Call(ctx, sandbox.Export, nil)
			over[i] = time.Since(s) - limit
			if err == nil {
				t.Fatal("infinite loop returned")
			}
			if _, err := inst.Call(ctx, sandbox.Export, nil); err != nil {
				dead++
			}
			inst.Close(ctx)
		}
		t.Logf("S7 timeout=%v  overshoot p50=%v p99=%v max=%v  dead after timeout=%d/%d",
			limit, percentile(over, 0.5).Round(time.Microsecond), percentile(over, 0.99).Round(time.Microsecond),
			slices.Max(over).Round(time.Microsecond), dead, len(over))
		m.Close(ctx)
	}
}

// S8: time until a memory bomb traps, by memory limit.
func TestSpikeMemoryBomb(t *testing.T) {
	requireSpike(t)
	ctx := context.Background()
	rt, _ := New(Options{})
	defer rt.Close(ctx)
	for _, pages := range []uint32{64, 256, 1024} {
		m, err := rt.Compile(ctx, sandboxtest.Fixture(t, "memory-bomb"), sandbox.ModuleSpec{MemoryMaxPages: pages, Timeout: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		ds := make([]time.Duration, 10)
		var lastErr error
		for i := range ds {
			inst, _ := m.Instantiate(ctx, nil)
			s := time.Now()
			_, lastErr = inst.Call(ctx, sandbox.Export, nil)
			ds[i] = time.Since(s)
			inst.Close(ctx)
		}
		t.Logf("S8 limit=%4d pages (%4d MiB)  time to trap p50=%v max=%v  err=%v",
			pages, pages/16, percentile(ds, 0.5).Round(time.Microsecond), slices.Max(ds).Round(time.Microsecond), lastErr)
		m.Close(ctx)
	}
}

// S11: which host functions each fixture imports.
func TestSpikeImports(t *testing.T) {
	requireSpike(t)
	for _, name := range []string{"discount", "counter", "echo-config", "guest-error", "infinite-loop", "memory-bomb", "bad-output", "http-call"} {
		info, err := wasminfo.Inspect(context.Background(), sandboxtest.Fixture(t, name), 0)
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(info.Imports))
		for _, imp := range info.Imports {
			names = append(names, imp.Name)
		}
		t.Logf("S11 %-14s imports=%v", name, names)
	}
}
