// Command demo proves the Milestone 1 claims against a running compose
// (spec M1 §8.2): a hook created through the control plane reaches the data
// plane without a restart, the data plane keeps serving while the control
// plane is down and picks up the next change once it is back, and (should
// tier) a module seeded into MinIO runs and module validation reports the
// right verdicts. It prints a table of step, expected, actual, time and
// PASS/FAIL and exits 1 if any step failed.
//
// Usage, from e2e/ with the compose up:
//
//	docker compose up -d --build --wait control-plane dataplane
//	go run ./cmd/demo
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/kudesn1k1/WasmHooks/e2e/internal/scenario"
)

func main() {
	// Ctrl-C cancels the run; the scenario still starts the control plane
	// again if it was stopped at that moment.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	passed, err := run(ctx, os.Args[1:], os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
	if !passed {
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) (bool, error) {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	composeDir := fs.String("compose-dir", "..", "directory with compose.yaml (the repository root when run from e2e/)")
	controlPlane := fs.String("control-plane", "http://localhost:8000", "control plane base URL")
	dataPlane := fs.String("data-plane", "http://localhost:8080", "data plane base URL")
	skipShould := fs.Bool("skip-should", false, "skip steps 5-7 (seed and module validation)")
	if err := fs.Parse(args); err != nil {
		return false, err
	}

	steps, err := scenario.Run(ctx, scenario.Config{
		ComposeDir:    *composeDir,
		ControlPlane:  *controlPlane,
		DataPlane:     *dataPlane,
		IncludeShould: !*skipShould,
	})
	if len(steps) > 0 {
		scenario.WriteTable(stdout, steps)
	}
	if err != nil {
		return false, err
	}
	return scenario.Passed(steps) == len(steps), nil
}
