// Package e2e runs the Milestone 1 scenario (spec M1 §8.2) against the
// compose in the repository root. It needs Docker and a running compose, so
// it is skipped unless E2E=1:
//
//	docker compose up -d --build --wait control-plane dataplane
//	cd e2e && E2E=1 go test -count=1 -v ./...
package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/kudesn1k1/WasmHooks/e2e/internal/scenario"
)

func TestScenario(t *testing.T) {
	if os.Getenv("E2E") != "1" {
		t.Skip("set E2E=1 with the compose up: docker compose up -d --build --wait control-plane dataplane")
	}
	steps, err := scenario.Run(t.Context(), scenario.Config{
		ComposeDir:    "..",
		ControlPlane:  "http://localhost:8000",
		DataPlane:     "http://localhost:8080",
		IncludeShould: true,
	})
	if len(steps) > 0 {
		var table strings.Builder
		scenario.WriteTable(&table, steps)
		t.Log("\n" + table.String())
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if !s.Pass {
			t.Errorf("step %s (%s): expected %s, got %s", s.N, s.Name, s.Expected, s.Actual)
		}
	}
}
