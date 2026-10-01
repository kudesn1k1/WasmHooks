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
	"github.com/tetratelabs/wazero/api"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox"
)

// Inspect parses wasm and lists its imports, exported functions and their
// signatures. When memoryMaxPages > 0, a module whose own memory needs more
// pages fails here. Invalid modules yield an error wrapping
// sandbox.ErrInvalidModule.
func Inspect(ctx context.Context, wasm []byte, memoryMaxPages uint32) (sandbox.ModuleInfo, error) {
	// The interpreter compiles cheaply: we only need the parsed module.
	cfg := wazero.NewRuntimeConfigInterpreter()
	if memoryMaxPages > 0 {
		cfg = cfg.WithMemoryLimitPages(memoryMaxPages)
	}
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	defer rt.Close(ctx)

	cm, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		return sandbox.ModuleInfo{}, fmt.Errorf("%w: %v", sandbox.ErrInvalidModule, err)
	}
	defer cm.Close(ctx)

	info := sandbox.ModuleInfo{Signatures: map[string]sandbox.Signature{}}
	for _, fn := range cm.ImportedFunctions() {
		module, name, _ := fn.Import()
		info.Imports = append(info.Imports, sandbox.Import{Module: module, Name: name})
	}
	for _, mem := range cm.ImportedMemories() {
		module, name, _ := mem.Import()
		info.MemoryImports = append(info.MemoryImports, sandbox.Import{Module: module, Name: name})
	}
	for name, fn := range cm.ExportedFunctions() {
		info.Exports = append(info.Exports, name)
		info.Signatures[name] = sandbox.Signature{Params: typeNames(fn.ParamTypes()), Results: typeNames(fn.ResultTypes())}
	}
	sort.Strings(info.Exports)
	return info, nil
}

func typeNames(types []api.ValueType) []string {
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = api.ValueTypeName(t)
	}
	return names
}
