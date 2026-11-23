package main

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/executor"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/observe"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/pool"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/extismrt"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/slots"
)

type localEngine struct {
	*executor.Executor
	pools   *pool.Manager
	runtime *extismrt.Runtime
}

func newEngine(f flags, cfg *config.Store, schemas *schema.Cache, log *slog.Logger) (*localEngine, error) {
	modules, err := moduleStore(f)
	if err != nil {
		return nil, err
	}

	rt, err := extismrt.New(extismrt.Options{})
	if err != nil {
		return nil, err
	}
	pools := pool.NewManager(pool.Options{
		GlobalMax:      f.poolGlobalMax,
		AcquireTimeout: f.poolAcquireTimeout,
		MaxUses:        f.poolMaxUses,
		IdleTTL:        f.poolIdleTTL,
		ReapInterval:   10 * time.Second,
	})
	sl := slots.New(slots.Options{Capacity: f.execSlots, TenantShare: f.execTenantShare, Queue: f.execQueue})
	log.Info("running slots", "slots", sl.Capacity(), "tenant_share", sl.TenantShare(), "queue", sl.Queue(), "gomaxprocs", runtime.GOMAXPROCS(0))
	if sl.TenantShare() >= sl.Capacity() {
		// One slot or a share of every slot: a tenant with hung scripts can
		// hold them all (ADR 0005).
		log.Warn("tenant isolation is off: one tenant may hold every running slot; it needs -exec-tenant-share below -exec-slots")
	}
	if sl.Capacity() >= runtime.GOMAXPROCS(0) {
		log.Warn("no P is left for the deadline watcher: timeouts of spinning scripts may overrun; keep -exec-slots below GOMAXPROCS where there are 3 or more")
	}

	exec := executor.New(executor.Options{
		Runtime:    rt,
		Modules:    modules,
		Config:     cfg,
		Pools:      pools,
		Slots:      sl,
		Schemas:    schemas,
		Sink:       observe.SlogSink{Logger: log},
		ExecutorID: f.executorID,
		OnCompile: func(hash string) {
			log.Info("module compiled", "module_hash", hash, "executor_id", f.executorID)
		},
		ValidateConcurrency: f.validateConcurrency,
	})

	return &localEngine{exec, pools, rt}, nil
}

func (e *localEngine) close(ctx context.Context) error {
	return errors.Join(e.pools.Close(ctx), e.Executor.Close(ctx), e.runtime.Close(ctx))
}
