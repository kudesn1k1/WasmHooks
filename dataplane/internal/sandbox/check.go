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

// CheckModule verifies a module's static shape against its spec: the
// required export is present and every import is either an allowed kernel
// function or a host function granted by the hook. All violations are
// reported at once, wrapped in ErrInvalidModule.
func CheckModule(info ModuleInfo, spec ModuleSpec) error {
	var violations []string
	if !slices.Contains(info.Exports, Export) {
		violations = append(violations, fmt.Sprintf("missing export %q", Export))
	}
	for _, imp := range info.Imports {
		if !importAllowed(imp, spec) {
			violations = append(violations, fmt.Sprintf("import %s.%s is not allowed", imp.Module, imp.Name))
		}
	}
	if len(violations) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidModule, strings.Join(violations, "; "))
	}
	return nil
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
