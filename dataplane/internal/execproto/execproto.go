// Package execproto is the contract between the gateway and the executor.
//
// The types mirror a future protobuf definition one to one: only primitives,
// slices and maps, no pointers to internal structures, no functions. Today
// the gateway calls the executor in process; after the split an execproto
// gRPC client and server implement the same Executor interface, and neither
// side changes.
package execproto

import "context"

// Outcome is the result class of one invocation.
type Outcome string

const (
	OutcomeOK            Outcome = "ok"
	OutcomeNoHandler     Outcome = "no_handler"
	OutcomeTimeout       Outcome = "timeout"
	OutcomeHandlerError  Outcome = "handler_error"
	OutcomeQuotaExceeded Outcome = "quota_exceeded"
	OutcomeUnavailable   Outcome = "unavailable"
)

// Reasons refine an outcome for logs, metrics and operators.
const (
	ReasonTrap          = "trap"
	ReasonGuestError    = "guest_error"
	ReasonInvalidOutput = "invalid_output"
	ReasonInvalidEffect = "invalid_effect"
	ReasonInvalidModule = "invalid_module"
	ReasonPoolSaturated = "pool_saturated"
	ReasonDeadline      = "deadline_before_start"
	ReasonModuleFetch   = "module_fetch"
	ReasonStaleConfig   = "stale_config"
	ReasonInternal      = "internal"
)

// ExecuteRequest asks the executor to run the tenant's script bound to a
// hook. The gateway pins every version it resolved, so both sides agree on
// what runs even when their config snapshots differ.
type ExecuteRequest struct {
	TenantID       string
	Hook           string
	HookDefVersion int64
	ModuleHash     string
	ConfigVersion  int64
	Payload        []byte // JSON matching the hook's input schema
	IdempotencyKey string
}

// Effect is a change the script asks its host to apply.
type Effect struct {
	Type    string
	Key     string
	Payload []byte // JSON
}

// ExecuteResult describes what happened.
type ExecuteResult struct {
	Outcome   Outcome
	Reason    string
	Result    []byte // JSON matching the hook's output schema; ok only
	Effects   []Effect
	Error     string // human readable, bounded
	ExecNanos int64  // time spent inside the script
	ColdStart bool   // an instance was created for this call
	Logs      []string
}

// Executor runs one invocation. The deadline travels in ctx. A non-nil
// error means the transport failed and nothing is known about execution;
// the in-process implementation never returns one.
type Executor interface {
	Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error)
}
