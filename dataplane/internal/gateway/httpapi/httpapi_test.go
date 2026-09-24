package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/gateway"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
)

const (
	token    = "whk_test_token"
	hookName = "checkout.discount"
	hash     = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	path     = "/v1/hooks/" + hookName + "/invoke"
)

type fakeExecutor struct {
	mu       sync.Mutex
	res      execproto.ExecuteResult
	deadline time.Duration
}

func (f *fakeExecutor) Execute(ctx context.Context, _ execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
	return f.res, nil
}

func newServer(t *testing.T, res execproto.ExecuteResult, ready func() bool) (*httptest.Server, *fakeExecutor) {
	t.Helper()
	sum := sha256.Sum256([]byte(token))
	cfg := config.NewStore()
	if err := cfg.Update(&config.Snapshot{
		Version: 1,
		APIKeys: []config.APIKey{{ID: "k", Name: "test", SHA256: hex.EncodeToString(sum[:])}},
		Hooks: []config.HookDef{{
			Name: hookName, DefVersion: 1,
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"cart_total":{"type":"number"}}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
			TimeoutMS:    50, MemoryMaxPages: 64,
		}},
		Tenants:  []config.Tenant{{ExternalID: "a"}},
		Bindings: []config.Binding{{TenantID: "a", Hook: hookName, ModuleHash: hash, ConfigVersion: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	exec := &fakeExecutor{res: res}
	srv := httptest.NewServer(New(gateway.New(cfg, exec, schema.NewCache(), gateway.Options{}), Options{Ready: ready}))
	t.Cleanup(srv.Close)
	return srv, exec
}

func post(t *testing.T, srv *httptest.Server, url, auth, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+url, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("response body is not JSON: %s", raw)
		}
	}
	return resp, out
}

const okBody = `{"tenant_id":"a","payload":{"cart_total":1}}`

var bearer = "Bearer " + token

func TestAuth(t *testing.T) {
	srv, _ := newServer(t, execproto.ExecuteResult{Outcome: execproto.OutcomeOK}, nil)
	for _, auth := range []string{"", "Bearer wrong", "Basic " + token, token} {
		resp, body := post(t, srv, path, auth, okBody)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("auth %q: status %d", auth, resp.StatusCode)
		}
		if resp.Header.Get("Content-Type") != "application/problem+json" || body["status"] != float64(401) {
			t.Fatalf("auth %q: not a problem response: %v", auth, body)
		}
	}
	if resp, _ := post(t, srv, path, "bearer "+token, okBody); resp.StatusCode != http.StatusOK {
		t.Fatalf("scheme must be case-insensitive: %d", resp.StatusCode)
	}
}

func TestRequestErrors(t *testing.T) {
	srv, _ := newServer(t, execproto.ExecuteResult{Outcome: execproto.OutcomeOK}, nil)
	cases := []struct {
		name   string
		url    string
		body   string
		status int
		detail string
	}{
		{"unknown hook", "/v1/hooks/nope/invoke", okBody, 404, "nope"},
		{"empty body", path, ``, 400, "JSON"},
		{"not json", path, `hello`, 400, "JSON"},
		{"array body", path, `[1]`, 400, "JSON"},
		{"missing tenant", path, `{"payload":{}}`, 400, "tenant_id"},
		{"blank tenant", path, `{"tenant_id":"  ","payload":{}}`, 400, "tenant_id"},
		{"missing payload", path, `{"tenant_id":"a"}`, 400, "payload"},
		{"payload array", path, `{"tenant_id":"a","payload":[1]}`, 400, "payload"},
		{"payload string", path, `{"tenant_id":"a","payload":"x"}`, 400, "payload"},
		{"payload null", path, `{"tenant_id":"a","payload":null}`, 400, "payload"},
		{"deadline zero", path, `{"tenant_id":"a","payload":{},"deadline_ms":0}`, 400, "deadline_ms"},
		{"deadline negative", path, `{"tenant_id":"a","payload":{},"deadline_ms":-5}`, 400, "deadline_ms"},
		{"schema violation", path, `{"tenant_id":"a","payload":{"cart_total":"x"}}`, 400, "cart_total"},
		{"too large", path, `{"tenant_id":"a","payload":{"s":"` + strings.Repeat("x", 1<<20) + `"}}`, 413, "size"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := post(t, srv, tc.url, bearer, tc.body)
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d (%v)", resp.StatusCode, tc.status, body)
			}
			if resp.Header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
			}
			if detail, _ := body["detail"].(string); !strings.Contains(detail, tc.detail) {
				t.Fatalf("detail %q does not mention %q", detail, tc.detail)
			}
			if resp.Header.Get(OutcomeHeader) != "" {
				t.Fatal("request errors carry no outcome")
			}
		})
	}
}

func TestUnknownFieldsIgnored(t *testing.T) {
	srv, _ := newServer(t, execproto.ExecuteResult{Outcome: execproto.OutcomeOK, Result: []byte(`{}`)}, nil)
	if resp, _ := post(t, srv, path, bearer, `{"tenant_id":"a","payload":{},"future_field":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestDeadlineClamped(t *testing.T) {
	srv, exec := newServer(t, execproto.ExecuteResult{Outcome: execproto.OutcomeOK, Result: []byte(`{}`)}, nil)
	post(t, srv, path, bearer, `{"tenant_id":"a","payload":{},"deadline_ms":99999999}`)
	if exec.deadline > 30*time.Second || exec.deadline < 29*time.Second {
		t.Fatalf("deadline %v, want clamped to 30s", exec.deadline)
	}
}

func TestOutcomeMapping(t *testing.T) {
	cases := []struct {
		res     execproto.ExecuteResult
		status  int
		hasHash bool
		hasErr  bool
	}{
		{execproto.ExecuteResult{Outcome: execproto.OutcomeOK, Result: []byte(`{"d":1}`), Effects: []execproto.Effect{{Type: "notify", Key: "k:0", Payload: []byte(`{}`)}}}, 200, true, false},
		{execproto.ExecuteResult{Outcome: execproto.OutcomeTimeout}, 200, true, false},
		{execproto.ExecuteResult{Outcome: execproto.OutcomeHandlerError, Reason: execproto.ReasonTrap, Error: "wasm trap"}, 200, true, true},
		{execproto.ExecuteResult{Outcome: execproto.OutcomeUnavailable, Reason: execproto.ReasonPoolSaturated, Error: "busy"}, 503, false, true},
		{execproto.ExecuteResult{Outcome: execproto.OutcomeQuotaExceeded}, 429, false, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.res.Outcome), func(t *testing.T) {
			srv, _ := newServer(t, tc.res, nil)
			resp, body := post(t, srv, path, bearer, okBody)
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.status)
			}
			if got := resp.Header.Get(OutcomeHeader); got != string(tc.res.Outcome) {
				t.Fatalf("%s = %q", OutcomeHeader, got)
			}
			if body["outcome"] != string(tc.res.Outcome) {
				t.Fatalf("body outcome = %v", body["outcome"])
			}
			for _, field := range []string{"outcome", "result", "effects", "error", "duration_ms", "module_hash"} {
				if _, ok := body[field]; !ok {
					t.Fatalf("field %q missing: %v", field, body)
				}
			}
			if effects, ok := body["effects"].([]any); !ok {
				t.Fatalf("effects must be an array, got %v", body["effects"])
			} else if tc.res.Outcome == execproto.OutcomeOK && len(effects) != 1 {
				t.Fatalf("effects = %v", effects)
			}
			if (body["module_hash"] != nil) != tc.hasHash {
				t.Fatalf("module_hash = %v", body["module_hash"])
			}
			if (body["error"] != nil) != tc.hasErr {
				t.Fatalf("error = %v", body["error"])
			}
			if tc.res.Outcome == execproto.OutcomeOK {
				if r, _ := body["result"].(map[string]any); r["d"] != float64(1) {
					t.Fatalf("result = %v", body["result"])
				}
			} else if body["result"] != nil {
				t.Fatalf("result must be null, got %v", body["result"])
			}
		})
	}
}

func TestNoHandler(t *testing.T) {
	srv, _ := newServer(t, execproto.ExecuteResult{Outcome: execproto.OutcomeOK}, nil)
	resp, body := post(t, srv, path, bearer, `{"tenant_id":"ghost","payload":{}}`)
	if resp.StatusCode != 200 || body["outcome"] != "no_handler" || resp.Header.Get(OutcomeHeader) != "no_handler" {
		t.Fatalf("status %d body %v", resp.StatusCode, body)
	}
}

func TestHealthAndReadiness(t *testing.T) {
	var mu sync.Mutex
	ready := false
	srv, _ := newServer(t, execproto.ExecuteResult{}, func() bool { mu.Lock(); defer mu.Unlock(); return ready })
	get := func(p string) int {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if get("/healthz") != 200 {
		t.Fatal("healthz")
	}
	if get("/readyz") != 503 {
		t.Fatal("readyz before ready")
	}
	mu.Lock()
	ready = true
	mu.Unlock()
	if get("/readyz") != 200 {
		t.Fatal("readyz after ready")
	}
}

func TestBrokenInputSchemaIsUnavailable(t *testing.T) {
	sum := sha256.Sum256([]byte(token))
	cfg := config.NewStore()
	cfg.Update(&config.Snapshot{
		Version: 1,
		APIKeys: []config.APIKey{{ID: "k", Name: "test", SHA256: hex.EncodeToString(sum[:])}},
		Hooks: []config.HookDef{{
			Name: hookName, DefVersion: 1,
			InputSchema:  json.RawMessage(`{"type":5}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
			TimeoutMS:    50, MemoryMaxPages: 64,
		}},
	})
	srv := httptest.NewServer(New(gateway.New(cfg, &fakeExecutor{}, schema.NewCache(), gateway.Options{}), Options{}))
	defer srv.Close()
	resp, body := post(t, srv, path, bearer, okBody)
	if resp.StatusCode != http.StatusServiceUnavailable || body["outcome"] != "unavailable" || resp.Header.Get(OutcomeHeader) != "unavailable" {
		t.Fatalf("status %d body %v", resp.StatusCode, body)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv, _ := newServer(t, execproto.ExecuteResult{}, nil)
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
