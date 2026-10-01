// Command dataplane runs the WasmHooks data plane: gateway and executor in
// one process (Milestone 0). It serves the public API from
// api/invoke.openapi.yaml, loads its config snapshot from a file in the
// format of api/dataplane-internal.openapi.yaml and reads modules from a
// directory of <sha256-hex>.wasm files.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/executor"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/gateway"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/gateway/httpapi"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/observe"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/pool"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/extismrt"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "dataplane:", err)
		os.Exit(1)
	}
}

type flags struct {
	listen, snapshot, modulesDir, logLevel, executorID string

	poolGlobalMax      int
	poolAcquireTimeout time.Duration
	poolMaxUses        int
	poolIdleTTL        time.Duration
}

func parseFlags(args []string) (flags, error) {
	host, _ := os.Hostname()
	var f flags
	fs := flag.NewFlagSet("dataplane", flag.ContinueOnError)
	fs.StringVar(&f.listen, "listen", ":8080", "address to serve the public API on")
	fs.StringVar(&f.snapshot, "snapshot", "", "path to the config snapshot JSON (required)")
	fs.StringVar(&f.modulesDir, "modules-dir", "", "directory with <sha256-hex>.wasm modules (required)")
	fs.StringVar(&f.logLevel, "log-level", "info", "debug, info, warn or error")
	fs.StringVar(&f.executorID, "executor-id", host, "executor identity in invocation records")
	fs.IntVar(&f.poolGlobalMax, "pool-global-max", 256, "max live instances per process")
	fs.DurationVar(&f.poolAcquireTimeout, "pool-acquire-timeout", 10*time.Millisecond, "max wait for a free instance")
	fs.IntVar(&f.poolMaxUses, "pool-max-uses", 1000, "calls before an instance is recycled")
	fs.DurationVar(&f.poolIdleTTL, "pool-idle-ttl", 60*time.Second, "idle instances older than this are closed")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if f.snapshot == "" || f.modulesDir == "" {
		return f, errors.New("-snapshot and -modules-dir are required")
	}
	return f, nil
}

// run starts the data plane and blocks until ctx is done. It prints
// "listening on <addr>" to stdout once the listener is bound.
func run(ctx context.Context, args []string, stdout io.Writer) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(f.logLevel)); err != nil {
		return fmt.Errorf("-log-level: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	snap, err := config.FileSource{Path: f.snapshot}.Load(ctx)
	if err != nil {
		return fmt.Errorf("load snapshot: %w", err)
	}
	cfg := config.NewStore()
	if err := cfg.Update(snap); err != nil {
		return err
	}

	rt, err := extismrt.New(extismrt.Options{})
	if err != nil {
		return err
	}
	pools := pool.NewManager(pool.Options{
		GlobalMax:      f.poolGlobalMax,
		AcquireTimeout: f.poolAcquireTimeout,
		MaxUses:        f.poolMaxUses,
		IdleTTL:        f.poolIdleTTL,
		ReapInterval:   10 * time.Second,
	})
	schemas := schema.NewCache()
	exec := executor.New(executor.Options{
		Runtime:    rt,
		Modules:    modstore.NewFS(f.modulesDir),
		Config:     cfg,
		Pools:      pools,
		Schemas:    schemas,
		Sink:       observe.SlogSink{Logger: log},
		ExecutorID: f.executorID,
	})
	gw := gateway.New(cfg, exec, schemas, gateway.Options{})

	var ready atomic.Bool
	srv := &http.Server{
		Handler:           httpapi.New(gw, httpapi.Options{Ready: ready.Load}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second, // bodies are at most 1 MiB
		WriteTimeout:      35 * time.Second,
	}
	ln, err := net.Listen("tcp", f.listen)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "listening on %s\n", ln.Addr())
	log.Info("snapshot loaded", "version", snap.Version, "hooks", len(snap.Hooks), "bindings", len(snap.Bindings))

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	// Compile bound modules before reporting ready, so first calls do not
	// pay for compilation. Broken bindings are logged and surface as
	// handler_error or unavailable when called.
	if err := exec.Preload(ctx); err != nil {
		log.Warn("preload finished with errors", "err", err)
	}
	ready.Store(true)
	log.Info("ready", "listen", ln.Addr().String())

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	ready.Store(false)
	// Let calls in flight finish: no hook runs longer than MaxTimeoutMS.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.MaxTimeoutMS*time.Millisecond+5*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	<-serveErr // http.ErrServerClosed after Shutdown
	return errors.Join(err, pools.Close(shutdownCtx), exec.Close(shutdownCtx), rt.Close(shutdownCtx))
}
