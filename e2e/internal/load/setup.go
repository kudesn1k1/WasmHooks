// Package load is the noisy-tenant load generator of Milestone 2 (spec M2
// §9.3): it sets two tenants up through the control plane API, drives an
// open-model load against the data plane in a quiet and a noisy phase, and
// checks that the noisy tenant does not hurt the quiet one.
package load

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kudesn1k1/WasmHooks/e2e/internal/cpapi"
)

// Config is everything a run needs; the thresholds are flags of the command
// with a margin for a noisy shared CI runner (spec M2 §9.3).
type Config struct {
	ComposeDir   string // directory with compose.yaml
	FixturesDir  string // directory with discount.wasm
	ControlPlane string // http://localhost:8000
	DataPlane    string // http://localhost:8080

	Phase        time.Duration // length of the measured part of each phase
	Warmup       time.Duration // run before each phase, not measured
	ARPS, BRPS   int           // calls per second of merchant-a and merchant-b
	BQuota       int           // rate_limit_rps of merchant-b
	CallTimeout  time.Duration // client timeout of one call
	MaxP99Growth time.Duration // merchant-a p99 noisy may exceed p99 quiet by this much
	MinBShare    float64       // least share of quota_exceeded among merchant-b's calls
}

// Env is what Setup created.
type Env struct {
	Hook, APIKey     string
	TenantA, TenantB string
}

const (
	validateLimit = 15 * time.Second // background validation of an upload
	readyLimit    = 15 * time.Second // from activation to the first ok
)

// payload is the input of checkout.discount in examples/demo/snapshot.json.
const payload = `{"cart_total":9999,"customer":{"id":"c1","lifetime_spend":1500}}`

// Setup creates an operator key, a hook and the tenants merchant-a (no rate
// limit) and merchant-b (cfg.BQuota), each with discount.wasm uploaded,
// validated and activated, and waits until both run in the data plane. Names
// carry a per-run suffix, so a run does not depend on earlier ones.
func Setup(ctx context.Context, cfg Config) (Env, error) {
	demo, err := cpapi.LoadDemoHook(filepath.Join(cfg.ComposeDir, "examples", "demo", "snapshot.json"))
	if err != nil {
		return Env{}, err
	}
	wasm, err := os.ReadFile(filepath.Join(cfg.FixturesDir, "discount.wasm"))
	if err != nil {
		return Env{}, err
	}
	suffix := cpapi.Suffix()
	env := Env{
		Hook:    "checkout.discount_" + suffix,
		TenantA: "merchant-a-" + suffix,
		TenantB: "merchant-b-" + suffix,
	}
	if env.APIKey, err = cpapi.CreateAPIKey(ctx, cfg.ComposeDir, "loadgen-"+suffix); err != nil {
		return Env{}, err
	}
	cp := &cpapi.Client{BaseURL: cfg.ControlPlane, HTTP: &http.Client{Timeout: 10 * time.Second}, OperatorKey: env.APIKey}
	if err := cp.CreateHook(ctx, env.Hook, demo); err != nil {
		return Env{}, err
	}
	for _, t := range []struct {
		id  string
		rps int
	}{{env.TenantA, 0}, {env.TenantB, cfg.BQuota}} {
		if err := cp.CreateTenant(ctx, t.id, t.rps); err != nil {
			return Env{}, err
		}
		if _, err := cp.DeployModule(ctx, t.id, env.Hook, "discount.wasm", wasm, validateLimit); err != nil {
			return Env{}, fmt.Errorf("%s: %w", t.id, err)
		}
	}
	// The key, the hook, the tenants and the bindings reach the data plane by
	// long-poll; until then calls are 401, 404 or no_handler, and the first
	// run of a module may be unavailable.
	probe := &caller{client: &http.Client{Timeout: cfg.CallTimeout}, dataPlane: cfg.DataPlane, key: env.APIKey, hook: env.Hook}
	for _, tenant := range []string{env.TenantA, env.TenantB} {
		if err := probe.waitOK(ctx, tenant); err != nil {
			return Env{}, err
		}
	}
	return env, nil
}

// caller sends invoke calls for a tenant over its own client.
type caller struct {
	client    *http.Client
	dataPlane string
	key, hook string
}

// call returns the outcome (X-Hook-Outcome, else "http_<status>", else
// "error") and the time from sending the request to reading its response.
func (c *caller) call(ctx context.Context, tenant string) (string, time.Duration) {
	body := fmt.Sprintf(`{"tenant_id":%q,"payload":%s}`, tenant, payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.dataPlane+"/v1/hooks/"+c.hook+"/invoke", bytes.NewReader([]byte(body)))
	if err != nil {
		return "error", 0
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return "error", time.Since(start)
	}
	// Draining the body lets the connection go back to the idle pool.
	_, err = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	took := time.Since(start)
	if err != nil {
		return "error", took
	}
	if outcome := resp.Header.Get("X-Hook-Outcome"); outcome != "" {
		return outcome, took
	}
	return fmt.Sprintf("http_%d", resp.StatusCode), took
}

// waitOK calls every 50 ms until the tenant's call is ok.
func (c *caller) waitOK(ctx context.Context, tenant string) error {
	deadline := time.Now().Add(readyLimit)
	for {
		outcome, _ := c.call(ctx, tenant)
		if outcome == "ok" {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: still %s after %s", tenant, outcome, readyLimit)
		}
		t := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
