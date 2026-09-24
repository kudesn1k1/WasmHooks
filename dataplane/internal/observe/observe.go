// Package observe records invocations. In M0 records go to structured logs;
// later the same records land in the invocations store behind the consoles.
package observe

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// Invocation is the record of one call as seen by the executor.
type Invocation struct {
	TS             time.Time
	TenantID       string
	Hook           string
	HookDefVersion int64
	ModuleHash     string
	ConfigVersion  int64
	Outcome        string
	Reason         string
	Error          string
	Duration       time.Duration // whole Execute
	Exec           time.Duration // inside the script
	ColdStart      bool
	IdempotencyKey string
	Logs           []string
	ExecutorID     string
}

// Sink receives invocation records. Record must not block the caller for
// long: it runs on the request path.
type Sink interface {
	Record(Invocation)
}

// SlogSink writes one structured log line per invocation.
type SlogSink struct {
	Logger *slog.Logger
}

func (s SlogSink) Record(inv Invocation) {
	attrs := []slog.Attr{
		slog.String("tenant_id", inv.TenantID),
		slog.String("hook", inv.Hook),
		slog.Int64("hook_def_version", inv.HookDefVersion),
		slog.String("module_hash", inv.ModuleHash),
		slog.Int64("config_version", inv.ConfigVersion),
		slog.String("outcome", inv.Outcome),
		slog.Duration("duration", inv.Duration),
		slog.Duration("exec", inv.Exec),
		slog.Bool("cold_start", inv.ColdStart),
		slog.String("executor_id", inv.ExecutorID),
	}
	if inv.Reason != "" {
		attrs = append(attrs, slog.String("reason", inv.Reason))
	}
	if inv.Error != "" {
		attrs = append(attrs, slog.String("error", inv.Error))
	}
	if inv.IdempotencyKey != "" {
		attrs = append(attrs, slog.String("idempotency_key", inv.IdempotencyKey))
	}
	if len(inv.Logs) > 0 {
		attrs = append(attrs, slog.Any("logs", inv.Logs))
	}
	s.Logger.LogAttrs(context.Background(), slog.LevelInfo, "invocation", attrs...)
}

// MemorySink keeps records in memory, for tests.
type MemorySink struct {
	mu   sync.Mutex
	recs []Invocation
}

func (m *MemorySink) Record(inv Invocation) {
	m.mu.Lock()
	m.recs = append(m.recs, inv)
	m.mu.Unlock()
}

// All returns a copy of the records so far.
func (m *MemorySink) All() []Invocation {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.recs)
}
