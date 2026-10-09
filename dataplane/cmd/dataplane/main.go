// Command dataplane runs the WasmHooks data plane: gateway and executor in
// one process. It serves the public API from api/invoke.openapi.yaml.
//
// Config comes from a snapshot file (-snapshot, the Milestone 0 mode) or is
// long-polled from the control plane (-control-plane-url). Modules come from
// a directory of <sha256-hex>.wasm files (-modules-dir) or from S3
// (-s3-endpoint). Secrets come from the environment: WASMHOOKS_INTERNAL_TOKEN,
// WASMHOOKS_S3_ACCESS_KEY, WASMHOOKS_S3_SECRET_KEY.
//
// `dataplane probe <url>` exits 0 if url answers 200: a health check for
// images that have neither a shell nor curl.
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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/executor"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/gateway"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/gateway/httpapi"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/internalapi"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/observe"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/pool"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/extismrt"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "probe" {
		os.Exit(probe(os.Args[2]))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "dataplane:", err)
		os.Exit(1)
	}
}

// probe returns 0 if url answers 200 within two seconds, else 1.
func probe(url string) int {
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "probe:", resp.Status)
		return 1
	}
	return 0
}

type flags struct {
	listen, snapshot, modulesDir, logLevel, executorID string

	controlPlaneURL     string
	internalListen      string
	validateConcurrency int

	s3Endpoint, s3Bucket, s3Region string
	s3Insecure                     bool

	// From the environment, never from flags: flags show up in process lists.
	internalToken, s3AccessKey, s3SecretKey string

	poolGlobalMax      int
	poolAcquireTimeout time.Duration
	poolMaxUses        int
	poolIdleTTL        time.Duration
}

func parseFlags(args []string, getenv func(string) string) (flags, error) {
	host, _ := os.Hostname()
	var f flags
	fs := flag.NewFlagSet("dataplane", flag.ContinueOnError)
	fs.StringVar(&f.listen, "listen", ":8080", "address to serve the public API on")
	fs.StringVar(&f.snapshot, "snapshot", "", "path to a config snapshot JSON file (instead of -control-plane-url)")
	fs.StringVar(&f.modulesDir, "modules-dir", "", "directory with <sha256-hex>.wasm modules (instead of -s3-endpoint)")
	fs.StringVar(&f.logLevel, "log-level", "info", "debug, info, warn or error")
	fs.StringVar(&f.executorID, "executor-id", host, "executor identity in invocation records")
	fs.IntVar(&f.poolGlobalMax, "pool-global-max", 256, "max live instances per process")
	fs.DurationVar(&f.poolAcquireTimeout, "pool-acquire-timeout", 10*time.Millisecond, "max wait for a free instance")
	fs.IntVar(&f.poolMaxUses, "pool-max-uses", 1000, "calls before an instance is recycled")
	fs.DurationVar(&f.poolIdleTTL, "pool-idle-ttl", 60*time.Second, "idle instances older than this are closed")
	fs.StringVar(&f.controlPlaneURL, "control-plane-url", "", "control plane base URL; config snapshots are long-polled from it")
	fs.StringVar(&f.internalListen, "internal-listen", ":8081", "address of the internal API (module validation); started only with WASMHOOKS_INTERNAL_TOKEN")
	fs.IntVar(&f.validateConcurrency, "validate-concurrency", 1, "max module validations running at once")
	fs.StringVar(&f.s3Endpoint, "s3-endpoint", "", "S3 endpoint host:port; modules are fetched from S3 instead of -modules-dir")
	fs.StringVar(&f.s3Bucket, "s3-bucket", "modules", "S3 bucket with <sha256-hex>.wasm objects")
	fs.StringVar(&f.s3Region, "s3-region", "us-east-1", "S3 region")
	fs.BoolVar(&f.s3Insecure, "s3-insecure", false, "use plain HTTP for S3 (local MinIO)")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	f.internalToken = getenv("WASMHOOKS_INTERNAL_TOKEN")
	f.s3AccessKey = getenv("WASMHOOKS_S3_ACCESS_KEY")
	f.s3SecretKey = getenv("WASMHOOKS_S3_SECRET_KEY")

	switch {
	case (f.snapshot == "") == (f.controlPlaneURL == ""):
		return f, errors.New("exactly one of -snapshot and -control-plane-url is required")
	case (f.modulesDir == "") == (f.s3Endpoint == ""):
		return f, errors.New("exactly one of -modules-dir and -s3-endpoint is required")
	case f.controlPlaneURL != "" && f.internalToken == "":
		return f, errors.New("-control-plane-url needs WASMHOOKS_INTERNAL_TOKEN")
	case f.s3Endpoint != "" && (f.s3AccessKey == "" || f.s3SecretKey == ""):
		return f, errors.New("-s3-endpoint needs WASMHOOKS_S3_ACCESS_KEY and WASMHOOKS_S3_SECRET_KEY")
	}
	return f, nil
}

func moduleStore(f flags) (modstore.Store, error) {
	if f.s3Endpoint == "" {
		return modstore.NewFS(f.modulesDir), nil
	}
	return modstore.NewS3(modstore.S3Options{
		Endpoint:  f.s3Endpoint,
		Bucket:    f.s3Bucket,
		Region:    f.s3Region,
		AccessKey: f.s3AccessKey,
		SecretKey: f.s3SecretKey,
		Secure:    !f.s3Insecure,
	})
}

// run starts the data plane and blocks until ctx is done. It prints
// "listening on <addr>" to stdout once the listener is bound.
func run(ctx context.Context, args []string, stdout io.Writer) error {
	f, err := parseFlags(args, os.Getenv)
	if err != nil {
		return err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(f.logLevel)); err != nil {
		return fmt.Errorf("-log-level: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg := config.NewStore()
	if f.snapshot != "" {
		snap, err := config.FileSource{Path: f.snapshot}.Load(ctx)
		if err != nil {
			return fmt.Errorf("load snapshot: %w", err)
		}
		if err := cfg.Update(snap); err != nil {
			return err
		}
		log.Info("snapshot loaded", "version", snap.Version, "hooks", len(snap.Hooks), "bindings", len(snap.Bindings))
	}
	modules, err := moduleStore(f)
	if err != nil {
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
		Modules:    modules,
		Config:     cfg,
		Pools:      pools,
		Schemas:    schemas,
		Sink:       observe.SlogSink{Logger: log},
		ExecutorID: f.executorID,

		ValidateConcurrency: f.validateConcurrency,
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

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	// The internal API (module validation for the control plane) has its own
	// listener so an installation can publish the public port alone.
	var internal *http.Server
	internalErr := make(chan error, 1)
	if f.internalToken != "" {
		internal = &http.Server{
			Handler:           internalapi.New(exec, f.internalToken, log.With("component", "internalapi")),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      60 * time.Second, // a validation compiles a module and runs it once
		}
		iln, err := net.Listen("tcp", f.internalListen)
		if err != nil {
			srv.Close()
			return fmt.Errorf("internal listener: %w", err)
		}
		fmt.Fprintf(stdout, "internal listening on %s\n", iln.Addr())
		go func() { internalErr <- internal.Serve(iln) }()
	}

	// Background work that must finish before the runtime is closed.
	var background sync.WaitGroup
	if f.controlPlaneURL != "" {
		first := make(chan struct{})
		var firstOnce sync.Once
		preloadKick := make(chan struct{}, 1) // coalesces bursts: at most one pending preload
		poller := &config.Poller{
			Source: &config.HTTPSource{BaseURL: f.controlPlaneURL, Token: f.internalToken},
			Store:  cfg,
			Log:    log.With("component", "config"),
			OnUpdate: func(*config.Snapshot) {
				firstOnce.Do(func() { close(first) })
				select {
				case preloadKick <- struct{}{}:
				default:
				}
			},
		}
		background.Add(1)
		go func() {
			defer background.Done()
			poller.Run(ctx)
		}()
		// Not ready until the first snapshot: until then there are no hooks
		// and no API keys to serve with.
		select {
		case <-first:
		case <-ctx.Done():
		case err := <-serveErr:
			return err
		}
		background.Add(1)
		go func() {
			defer background.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case <-preloadKick:
					if err := exec.Preload(ctx); err != nil {
						log.Warn("preload finished with errors", "err", err)
					}
				}
			}
		}()
	}

	if ctx.Err() == nil {
		// Compile bound modules before reporting ready, so first calls do not
		// pay for compilation. Broken bindings are logged and surface as
		// handler_error or unavailable when called. Later snapshots are
		// preloaded in the background.
		if err := exec.Preload(ctx); err != nil {
			log.Warn("preload finished with errors", "err", err)
		}
		ready.Store(true)
		log.Info("ready", "listen", ln.Addr().String())
	}

	select {
	case err := <-serveErr:
		return err
	case err := <-internalErr:
		return fmt.Errorf("internal API: %w", err)
	case <-ctx.Done():
	}
	log.Info("shutting down")
	ready.Store(false)
	// Let calls in flight finish: no hook runs longer than MaxTimeoutMS.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.MaxTimeoutMS*time.Millisecond+5*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	<-serveErr // http.ErrServerClosed after Shutdown
	if internal != nil {
		err = errors.Join(err, internal.Shutdown(shutdownCtx))
		<-internalErr
	}
	background.Wait()
	return errors.Join(err, pools.Close(shutdownCtx), exec.Close(shutdownCtx), rt.Close(shutdownCtx))
}
