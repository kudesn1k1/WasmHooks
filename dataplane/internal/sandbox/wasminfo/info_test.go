package wasminfo

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/sandboxtest"
)

func TestInspectDiscount(t *testing.T) {
	info, err := Inspect(context.Background(), sandboxtest.Fixture(t, "discount"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(info.Exports, sandbox.Export) {
		t.Fatalf("exports %v lack %q", info.Exports, sandbox.Export)
	}
	if len(info.Imports) == 0 {
		t.Fatal("an Extism plugin must import kernel functions")
	}
	for _, imp := range info.Imports {
		if imp.Module != "extism:host/env" {
			t.Errorf("unexpected import module %q", imp.Module)
		}
	}
	if sig := info.Signatures[sandbox.Export]; len(sig.Params) != 0 || !slices.Equal(sig.Results, []string{"i32"}) {
		t.Fatalf("handle signature = %+v", sig)
	}
}

func TestInspectGarbage(t *testing.T) {
	if _, err := Inspect(context.Background(), []byte("not wasm"), 0); !errors.Is(err, sandbox.ErrInvalidModule) {
		t.Fatalf("want ErrInvalidModule, got %v", err)
	}
}

func TestInspectMemoryImport(t *testing.T) {
	wasm := sandboxtest.TestModule{
		HandleResults: []byte{sandboxtest.I32},
		MemoryImport:  &sandboxtest.FuncImport{Module: "env", Name: "memory"},
	}.Build()
	info, err := Inspect(context.Background(), wasm, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(info.MemoryImports, []sandbox.Import{{Module: "env", Name: "memory"}}) {
		t.Fatalf("memory imports = %+v", info.MemoryImports)
	}
}

func TestInspectSignatures(t *testing.T) {
	wasm := sandboxtest.TestModule{
		HandleParams:  []byte{sandboxtest.I32},
		HandleResults: []byte{sandboxtest.I64},
		FuncImports:   []sandboxtest.FuncImport{{Module: "extism:host/env", Name: "alloc", Params: []byte{sandboxtest.I32}, Results: []byte{sandboxtest.I32}}},
	}.Build()
	info, err := Inspect(context.Background(), wasm, 0)
	if err != nil {
		t.Fatal(err)
	}
	sig := info.Signatures[sandbox.Export]
	if !slices.Equal(sig.Params, []string{"i32"}) || !slices.Equal(sig.Results, []string{"i64"}) {
		t.Fatalf("handle signature = %+v", sig)
	}
}

func TestInspectMemoryOverLimit(t *testing.T) {
	wasm := sandboxtest.TestModule{HandleResults: []byte{sandboxtest.I32}, MemoryMin: 20}.Build()
	if _, err := Inspect(context.Background(), wasm, 64); err != nil {
		t.Fatalf("20 pages under a 64-page limit: %v", err)
	}
	if _, err := Inspect(context.Background(), wasm, 16); !errors.Is(err, sandbox.ErrInvalidModule) {
		t.Fatalf("20 pages over a 16-page limit: want ErrInvalidModule, got %v", err)
	}
}
