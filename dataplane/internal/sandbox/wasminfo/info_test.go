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
	info, err := Inspect(context.Background(), sandboxtest.Fixture(t, "discount"))
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
}

func TestInspectGarbage(t *testing.T) {
	_, err := Inspect(context.Background(), []byte("not wasm"))
	if !errors.Is(err, sandbox.ErrInvalidModule) {
		t.Fatalf("want ErrInvalidModule, got %v", err)
	}
}
