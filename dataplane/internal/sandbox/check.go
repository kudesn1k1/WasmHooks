package sandbox

import (
	"fmt"
	"slices"
	"strings"
)

const (
	kernelModule = "extism:host/env"
	userModule   = "extism:host/user"
)

// kernelAllowed lists Extism kernel functions every script may import:
// memory and I/O plumbing, errors, config, vars, logging. HTTP is absent on
// purpose: scripts do not perform network I/O unless a hook grants it.
var kernelAllowed = map[string]bool{
	"alloc": true, "free": true, "length": true, "length_unsafe": true,
	"load_u8": true, "load_u64": true, "store_u8": true, "store_u64": true,
	"input_length": true, "input_load_u8": true, "input_load_u64": true, "input_offset": true,
	"output_set": true, "output_length": true, "output_offset": true,
	"error_set": true, "error_get": true, "reset": true, "memory_bytes": true,
	"config_get": true, "var_get": true, "var_set": true,
	"log_trace": true, "log_debug": true, "log_info": true, "log_warn": true, "log_error": true,
	"get_log_level": true,
}

// ModuleError lists every static violation of a module's spec. It matches
// ErrInvalidModule, and also ErrMissingExport or ErrForbiddenImport when the
// corresponding list is non-empty, so callers that only care whether a
// module is invalid keep using errors.Is(err, ErrInvalidModule), while module
// validation can report exports and imports as separate checks.
type ModuleError struct {
	Export  []string // problems with the required export
	Imports []string // forbidden function and memory imports
}

func (e *ModuleError) Error() string {
	return ErrInvalidModule.Error() + ": " + strings.Join(slices.Concat(e.Export, e.Imports), "; ")
}

func (e *ModuleError) Is(target error) bool {
	switch target {
	case ErrInvalidModule:
		return true
	case ErrMissingExport:
		return len(e.Export) > 0
	case ErrForbiddenImport:
		return len(e.Imports) > 0
	}
	return false
}

// CheckModule verifies a module's static shape against its spec: the
// required export is present with type () -> i32 (or () -> ()), and every
// import is either an allowed kernel function or a host function granted by
// the hook. Imported memories are never allowed. All violations are reported
// at once, as a *ModuleError.
func CheckModule(info ModuleInfo, spec ModuleSpec) error {
	var e ModuleError
	if !slices.Contains(info.Exports, Export) {
		e.Export = append(e.Export, fmt.Sprintf("missing export %q", Export))
	} else if sig, ok := info.Signatures[Export]; ok && !validHandle(sig) {
		e.Export = append(e.Export, fmt.Sprintf("export %q must have type () -> i32, has (%s) -> (%s)",
			Export, strings.Join(sig.Params, ", "), strings.Join(sig.Results, ", ")))
	}
	for _, imp := range info.Imports {
		if !importAllowed(imp, spec) {
			e.Imports = append(e.Imports, fmt.Sprintf("import %s.%s is not allowed", imp.Module, imp.Name))
		}
	}
	for _, imp := range info.MemoryImports {
		e.Imports = append(e.Imports, fmt.Sprintf("import %s.%s (memory) is not allowed", imp.Module, imp.Name))
	}
	if len(e.Export) > 0 || len(e.Imports) > 0 {
		return &e
	}
	return nil
}

func validHandle(sig Signature) bool {
	return len(sig.Params) == 0 && (len(sig.Results) == 0 || slices.Equal(sig.Results, []string{"i32"}))
}

func importAllowed(imp Import, spec ModuleSpec) bool {
	switch imp.Module {
	case kernelModule:
		return kernelAllowed[imp.Name]
	case userModule:
		return slices.Contains(spec.AllowedHostFunctions, imp.Name)
	default:
		return false
	}
}
