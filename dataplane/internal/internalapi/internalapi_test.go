package internalapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/executor"
)

const (
	token = "internal-token"
	path  = "/internal/v1/modules/validate"
	hash  = "sha256:1230c6a675e495a7bdcbc7695fe1344c0acd799d2dd07c1a2e01a071dd4ec724"
	hook  = `{"name":"checkout.discount","def_version":3,"input_schema":{},"output_schema":{},
		"timeout_ms":50,"memory_max_pages":64,"allowed_host_functions":[],"allowed_effect_types":[],
		"sample_input":{"x":1}}`
)

type fakeValidator struct {
	got    executor.ValidateRequest
	calls  int
	report executor.ValidationReport
	err    error
}

func (f *fakeValidator) Validate(_ context.Context, req executor.ValidateRequest) (executor.ValidationReport, error) {
	f.got, f.calls = req, f.calls+1
	return f.report, f.err
}

func do(t *testing.T, v Validator, method, auth, body string) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(New(v, token, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer srv.Close()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func TestValidateOK(t *testing.T) {
	f := &fakeValidator{report: executor.ValidationReport{OK: false, Checks: []executor.ValidationCheck{
		{Name: "fetch", OK: true}, {Name: "compile", OK: true}, {Name: "exports", OK: true},
		{Name: "imports", OK: false, Detail: "import extism:host/env.http_request is not allowed"},
		{Name: "sample_call", OK: false, Detail: "skipped"},
	}}}
	body := `{"module_hash":"` + hash + `","hook":` + hook + `,"config":{"k":"v"}}`
	resp, raw := do(t, f, http.MethodPost, "Bearer "+token, body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d %s: %s", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
	}
	if f.got.ModuleHash != hash || f.got.Hook.Name != "checkout.discount" || f.got.Hook.DefVersion != 3 || f.got.Config["k"] != "v" {
		t.Fatalf("validator got %+v", f.got)
	}
	// Field for field as in the contract; ok checks carry no detail.
	var got map[string]any
	json.Unmarshal([]byte(raw), &got)
	checks := got["checks"].([]any)
	if got["ok"] != false || len(checks) != 5 {
		t.Fatalf("body %s", raw)
	}
	if _, has := checks[0].(map[string]any)["detail"]; has {
		t.Fatalf("passed check has a detail: %s", raw)
	}
}

func TestValidateRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name, method, auth, body string
		status                   int
	}{
		{"no token", http.MethodPost, "", `{}`, http.StatusUnauthorized},
		{"wrong token", http.MethodPost, "Bearer nope", `{}`, http.StatusUnauthorized},
		{"wrong scheme", http.MethodPost, "Basic " + token, `{}`, http.StatusUnauthorized},
		{"GET", http.MethodGet, "Bearer " + token, ``, http.StatusMethodNotAllowed},
		{"not json", http.MethodPost, "Bearer " + token, `{`, http.StatusBadRequest},
		{"bad hash", http.MethodPost, "Bearer " + token, `{"module_hash":"sha256:XYZ","hook":` + hook + `}`, http.StatusBadRequest},
		{"no hook", http.MethodPost, "Bearer " + token, `{"module_hash":"` + hash + `"}`, http.StatusBadRequest},
		{"invalid hook", http.MethodPost, "Bearer " + token, `{"module_hash":"` + hash + `","hook":{"name":"h","def_version":0}}`, http.StatusBadRequest},
		{"too big", http.MethodPost, "Bearer " + token, `{"pad":"` + strings.Repeat("x", maxBodyBytes) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeValidator{}
			resp, raw := do(t, f, tt.method, tt.auth, tt.body)
			if resp.StatusCode != tt.status {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tt.status, raw)
			}
			if f.calls != 0 {
				t.Fatal("validator called for a rejected request")
			}
			if tt.status != http.StatusMethodNotAllowed && resp.Header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
			}
		})
	}
}

func TestValidateUnavailableIs503(t *testing.T) {
	f := &fakeValidator{err: errors.Join(executor.ErrUnavailable, errors.New("minio down"))}
	resp, raw := do(t, f, http.MethodPost, "Bearer "+token, `{"module_hash":"`+hash+`","hook":`+hook+`}`)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(raw, "minio down") {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
}
