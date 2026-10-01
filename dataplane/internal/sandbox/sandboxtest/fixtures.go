// Package sandboxtest holds the contract test suite every sandbox.Runtime
// implementation must pass, and helpers to load the wasm fixtures built from
// examples/scripts/rust.
package sandboxtest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// FixtureDir is the directory with the prebuilt fixture modules.
func FixtureDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "wasm")
}

// Fixture loads testdata/wasm/<name>.wasm.
func Fixture(tb testing.TB, name string) []byte {
	tb.Helper()
	wasm, err := os.ReadFile(filepath.Join(FixtureDir(), name+".wasm"))
	if err != nil {
		tb.Fatalf("load fixture %s: %v (rebuild with: go run ./tools/buildscripts)", name, err)
	}
	return wasm
}
