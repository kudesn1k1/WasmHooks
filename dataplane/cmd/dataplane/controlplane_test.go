package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/sandbox/sandboxtest"
)

// fakeControlPlane serves the snapshot long-poll from an in-memory snapshot.
// Before publish is first called it answers 503, like a control plane that
// is still starting.
type fakeControlPlane struct {
	mu      sync.Mutex
	snap    []byte
	version int64
	changed chan struct{}
}

func newFakeControlPlane() *fakeControlPlane {
	return &fakeControlPlane{changed: make(chan struct{})}
}

func (f *fakeControlPlane) publish(t *testing.T, version int64, hooks ...string) {
	t.Helper()
	sum := sha256.Sum256([]byte(e2eToken))
	hookDefs := []any{}
	for _, h := range hooks {
		hookDefs = append(hookDefs, map[string]any{
			"name": h, "def_version": 1, "input_schema": map[string]any{"type": "object"},
			"output_schema": map[string]any{"type": "object"}, "timeout_ms": 50, "memory_max_pages": 64,
			"allowed_host_functions": []string{}, "allowed_effect_types": []string{}, "sample_input": map[string]any{},
		})
	}
	raw, err := json.Marshal(map[string]any{
		"version":  version,
		"api_keys": []any{map[string]any{"id": "k1", "name": "e2e", "sha256": hex.EncodeToString(sum[:])}},
		"hooks":    hookDefs,
		"tenants":  []any{},
		"bindings": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.snap, f.version = raw, version
	close(f.changed)
	f.changed = make(chan struct{})
	f.mu.Unlock()
}

func (f *fakeControlPlane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer internal-test-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	after := int64(-1)
	if v := r.URL.Query().Get("after_version"); v != "" {
		after, _ = strconv.ParseInt(v, 10, 64)
	}
	for {
		f.mu.Lock()
		snap, version, changed := f.snap, f.version, f.changed
		f.mu.Unlock()
		switch {
		case snap == nil:
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		case version > after:
			w.Header().Set("Content-Type", "application/json")
			w.Write(snap)
			return
		}
		select {
		case <-changed:
		case <-time.After(time.Second):
			w.WriteHeader(http.StatusNotModified)
			return
		case <-r.Context().Done():
			return
		}
	}
}

// startDataPlane runs the data plane and returns the base URL of its public
// API and, when WASMHOOKS_INTERNAL_TOKEN is set, of its internal API.
func startDataPlane(t *testing.T, args ...string) (public, internal string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, append([]string{"-listen", "127.0.0.1:0", "-internal-listen", "127.0.0.1:0", "-log-level", "error"}, args...), pw)
		pw.Close()
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	})
	lines := bufio.NewReader(pr)
	read := func(prefix string) string {
		line, err := lines.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, prefix) {
			t.Fatalf("want a %q line, got %q (%v)", prefix, line, err)
		}
		return "http://" + strings.TrimSpace(strings.TrimPrefix(line, prefix))
	}
	public = read("listening on ")
	if os.Getenv("WASMHOOKS_INTERNAL_TOKEN") != "" {
		internal = read("internal listening on ")
	}
	go io.Copy(io.Discard, pr)
	return public, internal
}

func request(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e2eToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("X-Hook-Outcome")
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestControlPlaneMode(t *testing.T) {
	_, modules := writeEnv(t)
	fake := newFakeControlPlane()
	cp := httptest.NewServer(fake)
	t.Cleanup(cp.Close)
	t.Setenv("WASMHOOKS_INTERNAL_TOKEN", "internal-test-token")
	base, _ := startDataPlane(t, "-control-plane-url", cp.URL, "-modules-dir", modules)

	invoke := func(hook string) (int, string) {
		return request(t, http.MethodPost, base+"/v1/hooks/"+hook+"/invoke", `{"tenant_id":"merchant-a","payload":{}}`)
	}

	// Before the first snapshot: not ready, and calls are unavailable, not
	// unauthorized (the API keys come with the snapshot).
	if code, _ := request(t, http.MethodGet, base+"/readyz", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("readyz before snapshot = %d, want 503", code)
	}
	if code, outcome := invoke("checkout.discount"); code != http.StatusServiceUnavailable || outcome != "unavailable" {
		t.Fatalf("invoke before snapshot = %d/%q, want 503/unavailable", code, outcome)
	}

	fake.publish(t, 1, "checkout.discount")
	eventually(t, "ready", func() bool {
		code, _ := request(t, http.MethodGet, base+"/readyz", "")
		return code == http.StatusOK
	})
	if code, outcome := invoke("checkout.discount"); code != http.StatusOK || outcome != "no_handler" {
		t.Fatalf("invoke after v1 = %d/%q, want 200/no_handler", code, outcome)
	}
	if code, _ := invoke("order.validate"); code != http.StatusNotFound {
		t.Fatalf("unknown hook = %d, want 404", code)
	}

	// A new version reaches the running data plane without a restart.
	fake.publish(t, 2, "checkout.discount", "order.validate")
	eventually(t, "order.validate to appear", func() bool {
		code, outcome := invoke("order.validate")
		return code == http.StatusOK && outcome == "no_handler"
	})
}

func TestFlagValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"no config source", []string{"-modules-dir", "m"}, nil, "exactly one of -snapshot and -control-plane-url"},
		{"two config sources", []string{"-snapshot", "s", "-control-plane-url", "u", "-modules-dir", "m"}, map[string]string{"WASMHOOKS_INTERNAL_TOKEN": "t"}, "exactly one of -snapshot and -control-plane-url"},
		{"no module store", []string{"-snapshot", "s"}, nil, "exactly one of -modules-dir and -s3-endpoint"},
		{"two module stores", []string{"-snapshot", "s", "-modules-dir", "m", "-s3-endpoint", "e"}, nil, "exactly one of -modules-dir and -s3-endpoint"},
		{"control plane without token", []string{"-control-plane-url", "u", "-modules-dir", "m"}, nil, "WASMHOOKS_INTERNAL_TOKEN"},
		{"s3 without keys", []string{"-snapshot", "s", "-s3-endpoint", "e"}, nil, "WASMHOOKS_S3_ACCESS_KEY"},
		{"control plane and s3", []string{"-control-plane-url", "u", "-s3-endpoint", "e"}, map[string]string{
			"WASMHOOKS_INTERNAL_TOKEN": "t", "WASMHOOKS_S3_ACCESS_KEY": "a", "WASMHOOKS_S3_SECRET_KEY": "s",
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseFlags(tt.args, func(k string) string { return tt.env[k] })
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestProbe(t *testing.T) {
	for code, want := range map[int]int{http.StatusOK: 0, http.StatusServiceUnavailable: 1} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		if got := probe(srv.URL); got != want {
			t.Errorf("probe on %d = %d, want %d", code, got, want)
		}
		srv.Close()
	}
	if got := probe("http://127.0.0.1:1/"); got != 1 {
		t.Errorf("probe on a closed port = %d, want 1", got)
	}
}

func TestInternalValidate(t *testing.T) {
	snapshot, modules := writeEnv(t)
	t.Setenv("WASMHOOKS_INTERNAL_TOKEN", "internal-test-token")
	_, internal := startDataPlane(t, "-snapshot", snapshot, "-modules-dir", modules)

	hash := modstore.HashOf(sandboxtest.Fixture(t, "discount"))
	body := `{"module_hash":"` + hash + `","hook":{"name":"checkout.discount","def_version":1,
		"input_schema":{"type":"object"},"output_schema":{"type":"object"},"timeout_ms":2000,
		"memory_max_pages":64,"allowed_host_functions":[],"allowed_effect_types":[],
		"sample_input":{"cart_total":1,"customer":{"id":"c","lifetime_spend":5000}}}}`
	req, _ := http.NewRequest(http.MethodPost, internal+"/internal/v1/modules/validate", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer internal-test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var report struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name   string `json:"name"`
			OK     bool   `json:"ok"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decode report (status %d): %v", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || !report.OK {
		t.Fatalf("validate = %d ok=%v checks=%+v", resp.StatusCode, report.OK, report.Checks)
	}
}

func TestNoInternalListenerWithoutToken(t *testing.T) {
	snapshot, modules := writeEnv(t)
	t.Setenv("WASMHOOKS_INTERNAL_TOKEN", "")
	_, internal := startDataPlane(t, "-snapshot", snapshot, "-modules-dir", modules)
	if internal != "" {
		t.Fatalf("internal API started without a token at %s", internal)
	}
}
