// Package wasminfo reads the static shape of a WebAssembly module: imported
// and exported functions.
//
// wazero is used here as a wasm-format parser, not as the runtime: it is
// pure Go, needs no cgo and knows nothing about Extism. This is a deliberate
// coupling of the sandbox abstraction to wazero (ADR 0003).
package wasminfo

import (
	"context"
	"fmt"
	"sort"

	"github.com/tetratelabs/wazero"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
)

// Inspect parses wasm and lists its function imports and exports. Invalid
// modules yield an error wrapping sandbox.ErrInvalidModule.
func Inspect(ctx context.Context, wasm []byte) (sandbox.ModuleInfo, error) {
	// The interpreter compiles cheaply: we only need the parsed module.
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigInterpreter())
	defer rt.Close(ctx)

	cm, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		return sandbox.ModuleInfo{}, fmt.Errorf("%w: %v", sandbox.ErrInvalidModule, err)
	}
	defer cm.Close(ctx)

	var info sandbox.ModuleInfo
	for _, fn := range cm.ImportedFunctions() {
		module, name, _ := fn.Import()
		info.Imports = append(info.Imports, sandbox.Import{Module: module, Name: name})
	}
	for name := range cm.ExportedFunctions() {
		info.Exports = append(info.Exports, name)
	}
	sort.Strings(info.Exports)
	return info, nil
}
