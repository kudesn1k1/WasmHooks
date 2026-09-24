// Package sandbox defines the runtime-agnostic contract for executing tenant
// WebAssembly scripts: compile a module under a spec, instantiate it with a
// tenant's config, call its export. Implementations live in subpackages
// (extismrt) and must pass the sandboxtest contract suite.
package sandbox

import (
	"context"
	"errors"
	"time"
)

// Export is the single function every script exports, for every hook.
// The name is part of the language-agnostic script ABI.
const Export = "handle"

var (
	// ErrTimeout: execution exceeded its deadline and was interrupted.
	ErrTimeout = errors.New("sandbox: execution deadline exceeded")
	// ErrTrap: the module trapped (unreachable, out of bounds, failed allocation).
	ErrTrap = errors.New("sandbox: wasm trap")
	// ErrGuest: the script reported an error through its PDK.
	ErrGuest = errors.New("sandbox: guest returned error")
	// ErrClosed: the instance is no longer usable.
	ErrClosed = errors.New("sandbox: instance closed")
	// ErrInvalidModule: the module is not valid wasm or violates its ModuleSpec.
	ErrInvalidModule = errors.New("sandbox: module violates spec")
)

// Runtime compiles modules. It is safe for concurrent use.
type Runtime interface {
	Compile(ctx context.Context, wasm []byte, spec ModuleSpec) (Module, error)
	// Close releases every module compiled by this runtime.
	Close(ctx context.Context) error
}

// ModuleSpec carries the limits and capabilities a module is compiled under.
type ModuleSpec struct {
	// MemoryMaxPages caps linear memory, in 64 KiB pages. Required.
	MemoryMaxPages uint32
	// Timeout caps a single Call. Required.
	Timeout time.Duration
	// AllowedHostFunctions lists user host functions (extism:host/user)
	// the module may import.
	AllowedHostFunctions []string
}

// Module is a compiled script. It is safe for concurrent use; instances are not.
type Module interface {
	// Instantiate creates an isolated instance. cfg is copied: later changes
	// to the caller's map do not reach the instance.
	Instantiate(ctx context.Context, cfg map[string]string) (Instance, error)
	Close(ctx context.Context) error
}

// Instance is a single live copy of a module. It must be used by one caller
// at a time. Guest memory persists between calls.
type Instance interface {
	// Call runs export with input. After any error the instance must not be
	// reused; the caller closes it.
	Call(ctx context.Context, export string, input []byte) (CallResult, error)
	Close(ctx context.Context) error
}

// CallResult is the output of a successful call.
type CallResult struct {
	Output []byte
	Logs   []LogLine
}

// LogLine is one log record emitted by the script during a call.
type LogLine struct {
	Level   string // trace, debug, info, warn, error
	Message string
}

// Import is one function a module imports from its host.
type Import struct {
	Module string
	Name   string
}

// ModuleInfo is the static shape of a module.
type ModuleInfo struct {
	Imports []Import
	Exports []string // exported function names
}
