package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/sandboxtest"
)

const e2eToken = "whk_e2e"

// writeEnv lays out a snapshot and a modules directory for the demo tenants.
func writeEnv(t *testing.T) (snapshot, modules string) {
	t.Helper()
	dir := t.TempDir()
	modules = filepath.Join(dir, "modules")
	os.Mkdir(modules, 0o755)
	hashes := map[string]string{}
	for _, name := range []string{"discount", "infinite-loop", "memory-bomb"} {
		wasm := sandboxtest.Fixture(t, name)
		h := modstore.HashOf(wasm)
		hashes[name] = h
		os.WriteFile(filepath.Join(modules, strings.TrimPrefix(h, "sha256:")+".wasm"), wasm, 0o644)
	}
	sum := sha256.Sum256([]byte(e2eToken))
	snap := map[string]any{
		"version":  1,
		"api_keys": []any{map[string]any{"id": "k1", "name": "e2e", "sha256": hex.EncodeToString(sum[:])}},
		"hooks": []any{map[string]any{
			"name": "checkout.discount", "def_version": 1,
			"input_schema": map[string]any{
				"type": "object", "required": []string{"cart_total", "customer"},
				"properties": map[string]any{
					"cart_total": map[string]any{"type": "number"},
					"customer": map[string]any{
						"type": "object", "required": []string{"id", "lifetime_spend"},
						"properties": map[string]any{"id": map[string]any{"type": "string"}, "lifetime_spend": map[string]any{"type": "number"}},
					},
				},
			},
			"output_schema":          map[string]any{"type": "object", "required": []string{"discount_percent", "reason"}},
			"timeout_ms":             50,
			"memory_max_pages":       64,
			"allowed_host_functions": []string{},
			"allowed_effect_types":   []string{},
			"sample_input":           map[string]any{"cart_total": 1, "customer": map[string]any{"id": "c", "lifetime_spend": 0}},
		}},
		"tenants": []any{
			map[string]any{"external_id": "merchant-a", "concurrency_limit": 4},
			map[string]any{"external_id": "merchant-b", "concurrency_limit": 4},
			map[string]any{"external_id": "merchant-c", "concurrency_limit": 4},
		},
		"bindings": []any{
			map[string]any{"tenant_id": "merchant-a", "hook": "checkout.discount", "module_hash": hashes["discount"], "config": map[string]string{"threshold": "1000", "percent": "10"}, "config_version": 1},
			map[string]any{"tenant_id": "merchant-b", "hook": "checkout.discount", "module_hash": hashes["infinite-loop"], "config": map[string]string{}, "config_version": 1},
			map[string]any{"tenant_id": "merchant-c", "hook": "checkout.discount", "module_hash": hashes["memory-bomb"], "config": map[string]string{}, "config_version": 1},
		},
	}
	raw, _ := json.Marshal(snap)
	snapshot = filepath.Join(dir, "snapshot.json")
	os.WriteFile(snapshot, raw, 0o644)
	return snapshot, modules
}

func TestEndToEnd(t *testing.T) {
	snapshot, modules := writeEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"-listen", "127.0.0.1:0", "-snapshot", snapshot, "-modules-dir", modules, "-log-level", "error"}, pw)
		pw.Close()
	}()

	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("no listen line: %v", err)
	}
	go io.Copy(io.Discard, pr)
	base := "http://" + strings.TrimSpace(strings.TrimPrefix(line, "listening on "))

	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(base + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("data plane never became ready")
		}
		time.Sleep(20 * time.Millisecond)
	}

	invoke := func(tenant, payload, token string) (int, string, map[string]any) {
		body := `{"tenant_id":"` + tenant + `","payload":` + payload + `}`
		req, _ := http.NewRequest(http.MethodPost, base+"/v1/hooks/checkout.discount/invoke", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, resp.Header.Get("X-Hook-Outcome"), out
	}
	loyal := `{"cart_total":100,"customer":{"id":"c1","lifetime_spend":1500}}`

	steps := []struct {
		name, tenant, payload, token string
		status                       int
		outcome                      string
	}{
		{"discount", "merchant-a", loyal, e2eToken, 200, "ok"},
		{"infinite loop", "merchant-b", loyal, e2eToken, 200, "timeout"},
		{"still ok after timeout", "merchant-a", loyal, e2eToken, 200, "ok"},
		{"memory bomb", "merchant-c", loyal, e2eToken, 200, "handler_error"},
		{"no handler", "merchant-z", loyal, e2eToken, 200, "no_handler"},
		{"invalid payload", "merchant-a", `{"cart_total":1}`, e2eToken, 400, ""},
		{"bad key", "merchant-a", loyal, "wrong", 401, ""},
	}
	for _, s := range steps {
		status, outcome, body := invoke(s.tenant, s.payload, s.token)
		if status != s.status || outcome != s.outcome {
			t.Errorf("%s: got %d/%q, want %d/%q (%v)", s.name, status, outcome, s.status, s.outcome, body)
		}
		if s.name == "discount" {
			if r, _ := body["result"].(map[string]any); r["discount_percent"] != float64(10) {
				t.Errorf("discount result = %v", body["result"])
			}
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not stop")
	}
}
