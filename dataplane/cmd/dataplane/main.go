// Command dataplane runs the WasmHooks data plane: gateway and executor in
// one process by default or as independent roles. It serves the public API from api/invoke.openapi.yaml.
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
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
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
	role, discovery, rpcListen, advertised                         string
	discoveryInterval, healthTimeout, retryBudget, breakerCooldown time.Duration
	maxInFlight, breakerThreshold                                  int

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

	execSlots, execTenantShare, execQueue int
}

func parseFlags(args []string, getenv func(string) string) (flags, error) {
	host, _ := os.Hostname()
	var f flags
	fs := flag.NewFlagSet("dataplane", flag.ContinueOnError)
	fs.StringVar(&f.role, "role", "combined", "combined, gateway or executor")
	fs.StringVar(&f.discovery, "executors", "", "headless executor service host:port")
	fs.StringVar(&f.rpcListen, "rpc-listen", ":9090", "executor gRPC listener")
	fs.StringVar(&f.advertised, "executor-address", "", "executor identity on the ring (pod IP:port)")
	fs.DurationVar(&f.discoveryInterval, "discovery-interval", time.Second, "interval between DNS and health checks")
	fs.DurationVar(&f.healthTimeout, "health-timeout", 300*time.Millisecond, "timeout for an executor health check")
	fs.DurationVar(&f.retryBudget, "retry-min-budget", 15*time.Millisecond, "minimum remaining deadline for a retry")
	fs.DurationVar(&f.breakerCooldown, "breaker-cooldown", 5*time.Second, "time before a guest failure probe")
	fs.IntVar(&f.breakerThreshold, "breaker-threshold", 5, "consecutive guest failures to open the breaker; 0 disables it")
	fs.IntVar(&f.maxInFlight, "max-in-flight", 256, "gateway limit on unfinished invocations")
	fs.StringVar(&f.listen, "listen", ":8080", "address to serve the public API on")
	fs.StringVar(&f.snapshot, "snapshot", "", "path to a config snapshot JSON file (instead of -control-plane-url)")
	fs.StringVar(&f.modulesDir, "modules-dir", "", "directory with <sha256-hex>.wasm modules (instead of -s3-endpoint)")
	fs.StringVar(&f.logLevel, "log-level", "info", "debug, info, warn or error")
	fs.StringVar(&f.executorID, "executor-id", host, "executor identity in invocation records")
	fs.IntVar(&f.poolGlobalMax, "pool-global-max", 256, "max live instances per process")
	fs.DurationVar(&f.poolAcquireTimeout, "pool-acquire-timeout", 10*time.Millisecond, "max wait for a free instance")
	fs.IntVar(&f.poolMaxUses, "pool-max-uses", 1000, "calls before an instance is recycled")
	fs.DurationVar(&f.poolIdleTTL, "pool-idle-ttl", 60*time.Second, "idle instances older than this are closed")
	fs.IntVar(&f.execSlots, "exec-slots", 0, "max scripts running at once; 0: max(1, GOMAXPROCS-1)")
	fs.IntVar(&f.execTenantShare, "exec-tenant-share", 0, "max scripts of one tenant running at once; 0: half the slots")
	fs.IntVar(&f.execQueue, "exec-queue", 0, "max calls waiting for a slot; 0: 4 x slots")
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
	case f.role != "combined" && f.role != "gateway" && f.role != "executor":
		return f, errors.New("unknown data plane role")
	case f.role != "combined" && (f.discovery == "" || f.internalToken == ""):
		return f, errors.New("split roles need -executors and WASMHOOKS_INTERNAL_TOKEN")
	case f.role == "executor" && f.advertised == "":
		return f, errors.New("executor needs -executor-address")
	case f.discoveryInterval <= 0 || f.healthTimeout <= 0 || f.retryBudget <= 0 || f.breakerCooldown <= 0 || f.breakerThreshold < 0 || f.maxInFlight <= 0:
		return f, errors.New("invalid discovery, retry or gateway limits")
	case f.role != "gateway" && (f.modulesDir == "") == (f.s3Endpoint == ""):
		return f, errors.New("exactly one of -modules-dir and -s3-endpoint is required")
	case f.controlPlaneURL != "" && f.internalToken == "":
		return f, errors.New("-control-plane-url needs WASMHOOKS_INTERNAL_TOKEN")
	case f.s3Endpoint != "" && (f.s3AccessKey == "" || f.s3SecretKey == ""):
		return f, errors.New("-s3-endpoint needs WASMHOOKS_S3_ACCESS_KEY and WASMHOOKS_S3_SECRET_KEY")
	case f.execSlots < 0 || f.execTenantShare < 0 || f.execQueue < 0:
		return f, errors.New("-exec-slots, -exec-tenant-share and -exec-queue must be >= 0")
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
	return serveRoles(ctx, f, cfg, log, stdout)
}
