// Command wasmimports prints the imported and exported functions of
// WebAssembly modules. It is used to check that a script only imports host
// functions the platform provides and that it exports "handle".
//
// Usage (from dataplane/):
//
//	go run ./tools/wasmimports testdata/wasm/*.wasm
//
// Glob patterns are expanded by the tool itself, so the command also works in
// shells that do not expand them (PowerShell, cmd).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: wasmimports <file.wasm>...")
		os.Exit(2)
	}
	if err := run(context.Background(), os.Stdout, os.Args[1:]); err != nil {
		slog.Error("wasmimports failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, w io.Writer, args []string) error {
	paths, err := expand(args)
	if err != nil {
		return err
	}

	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigInterpreter())
	defer rt.Close(ctx)

	var errs []error
	for _, p := range paths {
		if err := describe(ctx, rt, w, p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// expand resolves glob patterns in args. An argument that matches nothing is
// kept as is, so a missing file is reported by describe.
func expand(args []string) ([]string, error) {
	var paths []string
	for _, a := range args {
		matches, err := filepath.Glob(a)
		if err != nil {
			return nil, fmt.Errorf("bad pattern %q: %w", a, err)
		}
		if len(matches) == 0 {
			paths = append(paths, a)
			continue
		}
		paths = append(paths, matches...)
	}
	return paths, nil
}

func describe(ctx context.Context, rt wazero.Runtime, w io.Writer, path string) error {
	bin, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	mod, err := rt.CompileModule(ctx, bin)
	if err != nil {
		return fmt.Errorf("compile %s: %w", path, err)
	}
	defer mod.Close(ctx)

	fmt.Fprintf(w, "%s\n", filepath.ToSlash(path))

	var imports []string
	for _, f := range mod.ImportedFunctions() {
		module, name, _ := f.Import()
		imports = append(imports, fmt.Sprintf("  import func %s.%s%s", module, name, signature(f)))
	}
	for _, m := range mod.ImportedMemories() {
		module, name, _ := m.Import()
		imports = append(imports, fmt.Sprintf("  import memory %s.%s", module, name))
	}
	slices.Sort(imports)

	exported := mod.ExportedFunctions()
	names := make([]string, 0, len(exported))
	for name := range exported {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, line := range imports {
		fmt.Fprintln(w, line)
	}
	for _, name := range names {
		fmt.Fprintf(w, "  export func %s%s\n", name, signature(exported[name]))
	}
	return nil
}

func signature(f api.FunctionDefinition) string {
	return "(" + typeList(f.ParamTypes()) + ") -> (" + typeList(f.ResultTypes()) + ")"
}

func typeList(types []api.ValueType) string {
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = api.ValueTypeName(t)
	}
	return strings.Join(names, ", ")
}
