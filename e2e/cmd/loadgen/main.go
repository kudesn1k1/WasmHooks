// Command loadgen is the noisy-tenant load test of Milestone 2 (spec M2
// §9.3) against a running compose. It sets up merchant-a and merchant-b
// through the control plane API, then runs two phases: quiet (merchant-a
// alone) and noisy (merchant-a with the same load plus merchant-b at 100
// times the rate). It prints calls, latency percentiles and outcomes per
// tenant and phase, and exits 1 on a FAIL line: merchant-a must stay 100% ok
// with p99 within a margin of the quiet phase, merchant-b must be mostly
// quota_exceeded and never time out.
//
// Usage, from e2e/ with the compose up:
//
//	docker compose up -d --build --wait control-plane dataplane
//	go run ./cmd/loadgen
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/kudesn1k1/WasmHooks/e2e/internal/load"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	passed, err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		os.Exit(1)
	}
	if !passed {
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) (bool, error) {
	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	var cfg load.Config
	fs.StringVar(&cfg.ComposeDir, "compose-dir", "..", "directory with compose.yaml (the repository root when run from e2e/)")
	fs.StringVar(&cfg.FixturesDir, "fixtures", "../dataplane/testdata/wasm", "directory with the wasm fixtures (discount.wasm)")
	fs.StringVar(&cfg.ControlPlane, "control-plane", "http://localhost:8000", "control plane base URL")
	fs.StringVar(&cfg.DataPlane, "data-plane", "http://localhost:8080", "data plane base URL")
	fs.DurationVar(&cfg.Phase, "phase", 10*time.Second, "length of the measured part of each phase")
	fs.DurationVar(&cfg.Warmup, "warmup", 2*time.Second, "unmeasured warmup before each phase")
	fs.IntVar(&cfg.ARPS, "a-rps", 50, "merchant-a calls per second")
	fs.IntVar(&cfg.BRPS, "b-rps", 5000, "merchant-b calls per second in the noisy phase")
	fs.IntVar(&cfg.BQuota, "b-quota", 50, "rate_limit_rps of merchant-b")
	fs.DurationVar(&cfg.CallTimeout, "call-timeout", 5*time.Second, "client timeout of one call")
	fs.DurationVar(&cfg.MaxP99Growth, "max-p99-growth", 10*time.Millisecond, "most merchant-a p99 may grow from the quiet to the noisy phase")
	fs.Float64Var(&cfg.MinBShare, "min-b-quota-share", 0.9, "least share of quota_exceeded among merchant-b's calls")
	if err := fs.Parse(args); err != nil {
		return false, err
	}

	fmt.Fprintln(stderr, "setup: key, hook, merchant-a and merchant-b with discount.wasm")
	env, err := load.Setup(ctx, cfg)
	if err != nil {
		return false, fmt.Errorf("setup: %w", err)
	}
	res, err := load.Run(ctx, cfg, env, func(s string) { fmt.Fprintln(stderr, s) })
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	load.WriteTable(stdout, res)
	violations := load.Violations(cfg, res)
	fmt.Fprintln(stdout)
	for _, v := range violations {
		fmt.Fprintln(stdout, "FAIL:", v)
	}
	if len(violations) == 0 {
		fmt.Fprintln(stdout, "PASS")
	}
	return len(violations) == 0, nil
}
