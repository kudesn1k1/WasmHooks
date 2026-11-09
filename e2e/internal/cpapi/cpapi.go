// Package cpapi is the part of the control plane API that the e2e programs
// need: an operator key from the CLI, hooks, tenants and quotas through the
// operator API, and the tenant path of a module (console session, upload,
// background validation, activation; Milestone 1 tasks T3-T5). The scenario
// and the load generator both set their tenants up with it.
//
// Request and response shapes follow api/control-plane.openapi.yaml.
package cpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultConfig is the binding config under which discount.wasm gives a
// 10% discount to a customer with lifetime_spend 1500 (the values seed-demo
// uses; the API takes config values as strings).
var DefaultConfig = map[string]string{"threshold": "1000", "percent": "10"}

// Client talks to the control plane. OperatorKey is the Bearer key of the
// operator routes; the tenant routes take a console token per call.
type Client struct {
	BaseURL     string
	HTTP        *http.Client
	OperatorKey string
}

// Error is a response with an unexpected status.
type Error struct {
	Op     string // "POST /api/v1/hooks"
	Status int
	Body   string // shortened to one line
}

func (e *Error) Error() string { return fmt.Sprintf("%s: HTTP %d %s", e.Op, e.Status, e.Body) }

// Suffix is a per-run suffix for hook, tenant and key names: lowercase
// base36 of the Unix seconds with a letter first, valid in a hook name segment.
func Suffix() string { return "r" + strconv.FormatInt(time.Now().Unix(), 36) }

// do sends in as JSON (nothing if nil) with the Bearer token, expects status
// want and decodes the response into out (if not nil).
func (c *Client) do(ctx context.Context, method, path, token string, in any, want int, out any) error {
	var body io.Reader
	contentType := ""
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, contentType = bytes.NewReader(data), "application/json"
	}
	return c.send(ctx, method, path, token, contentType, body, want, out)
}

func (c *Client) send(ctx context.Context, method, path, token, contentType string, body io.Reader, want int, out any) error {
	op := method + " " + path
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if resp.StatusCode != want {
		return &Error{Op: op, Status: resp.StatusCode, Body: short(data)}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s: decode response: %w", op, err)
		}
	}
	return nil
}

// short squeezes a response body into one line of at most 160 bytes.
func short(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

// --- operator --------------------------------------------------------------

// CreateAPIKey creates an operator API key with the control plane CLI inside
// the compose (there is no API for the first key) and returns its token.
func CreateAPIKey(ctx context.Context, composeDir, name string) (string, error) {
	out, err := ControlPlaneCLI(ctx, composeDir, "apikey", "create", "--name", name)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(out)
	if key == "" || strings.ContainsAny(key, " \n") {
		return "", fmt.Errorf("apikey create printed %q, want one token", out)
	}
	return key, nil
}

// HookSpec is the body of PUT /api/v1/hooks/{name} (HookSpec in the
// OpenAPI file); the API rejects unknown fields.
type HookSpec struct {
	InputSchema          map[string]any `json:"input_schema"`
	OutputSchema         map[string]any `json:"output_schema"`
	TimeoutMS            int            `json:"timeout_ms"`
	MemoryMaxPages       int            `json:"memory_max_pages"`
	AllowedHostFunctions []string       `json:"allowed_host_functions"`
	AllowedEffectTypes   []string       `json:"allowed_effect_types"`
	SampleInput          map[string]any `json:"sample_input"`
}

// CreateHook creates a hook (POST /api/v1/hooks, 201).
func (c *Client) CreateHook(ctx context.Context, name string, spec HookSpec) error {
	body := struct {
		Name string `json:"name"`
		HookSpec
	}{name, spec}
	return c.do(ctx, http.MethodPost, "/api/v1/hooks", c.OperatorKey, body, http.StatusCreated, nil)
}

// ReplaceHook replaces a hook's spec (PUT /api/v1/hooks/{name}, 200).
func (c *Client) ReplaceHook(ctx context.Context, name string, spec HookSpec) error {
	return c.do(ctx, http.MethodPut, "/api/v1/hooks/"+url.PathEscape(name), c.OperatorKey, spec, http.StatusOK, nil)
}

// CreateTenant creates a tenant (POST /api/v1/tenants, 201). rateLimitRPS 0
// means no rate limit; the concurrency limit is left at its default.
func (c *Client) CreateTenant(ctx context.Context, externalID string, rateLimitRPS int) error {
	body := map[string]any{"external_id": externalID, "name": externalID, "rate_limit_rps": rateLimitRPS}
	return c.do(ctx, http.MethodPost, "/api/v1/tenants", c.OperatorKey, body, http.StatusCreated, nil)
}

// PatchTenantRateLimit sets the tenant's rate_limit_rps (PATCH
// /api/v1/tenants/{external_id}, 200); nothing else changes.
func (c *Client) PatchTenantRateLimit(ctx context.Context, externalID string, rateLimitRPS int) error {
	body := map[string]any{"rate_limit_rps": rateLimitRPS}
	return c.do(ctx, http.MethodPatch, "/api/v1/tenants/"+url.PathEscape(externalID), c.OperatorKey, body, http.StatusOK, nil)
}

// ConsoleSession issues a console token for the tenant (POST
// /api/v1/tenants/{external_id}/console-sessions, 201).
func (c *Client) ConsoleSession(ctx context.Context, externalID string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	err := c.do(ctx, http.MethodPost, "/api/v1/tenants/"+url.PathEscape(externalID)+"/console-sessions",
		c.OperatorKey, nil, http.StatusCreated, &out)
	if err == nil && out.Token == "" {
		err = fmt.Errorf("console session for %s has no token", externalID)
	}
	return out.Token, err
}

// --- tenant ----------------------------------------------------------------

// Module is a module version (Module in the OpenAPI file).
type Module struct {
	ID               string          `json:"id"`
	ContentHash      string          `json:"content_hash"`
	Status           string          `json:"status"`
	ValidationReport json.RawMessage `json:"validation_report"`
}

// UploadModule uploads wasm as a new version of the hook's module (multipart
// field "file", 201). The module is then validated in the background.
func (c *Client) UploadModule(ctx context.Context, token, hook, filename string, wasm []byte) (Module, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return Module{}, err
	}
	if _, err := part.Write(wasm); err != nil {
		return Module{}, err
	}
	if err := mw.Close(); err != nil {
		return Module{}, err
	}
	var m Module
	err = c.send(ctx, http.MethodPost, "/api/v1/tenant/hooks/"+url.PathEscape(hook)+"/modules", token,
		mw.FormDataContentType(), &buf, http.StatusCreated, &m)
	return m, err
}

// WaitValidated polls the hook's module list every 100 ms until the module
// is validated. A rejected or archived module, or a status that is still not
// validated after limit, is an error.
func (c *Client) WaitValidated(ctx context.Context, token, hook, moduleID string, limit time.Duration) (Module, error) {
	deadline := time.Now().Add(limit)
	for {
		var list struct {
			Items []Module `json:"items"`
		}
		err := c.do(ctx, http.MethodGet, "/api/v1/tenant/hooks/"+url.PathEscape(hook)+"/modules", token, nil, http.StatusOK, &list)
		if err != nil {
			return Module{}, err
		}
		status := "missing from the list"
		for _, m := range list.Items {
			if m.ID != moduleID {
				continue
			}
			switch m.Status {
			case "validated":
				return m, nil
			case "rejected", "archived":
				return m, fmt.Errorf("module %s is %s: %s", moduleID, m.Status, short(m.ValidationReport))
			}
			status = m.Status
		}
		if time.Now().After(deadline) {
			return Module{}, fmt.Errorf("module %s still %s after %s", moduleID, status, limit)
		}
		t := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return Module{}, ctx.Err()
		case <-t.C:
		}
	}
}

// Activate makes the validated module the hook's active one and sets the
// binding config (PUT /api/v1/tenant/hooks/{hook}/binding, 200).
func (c *Client) Activate(ctx context.Context, token, hook, moduleID string, config map[string]string) error {
	body := map[string]any{"active_module_id": moduleID, "config": config}
	return c.do(ctx, http.MethodPut, "/api/v1/tenant/hooks/"+url.PathEscape(hook)+"/binding", token, body, http.StatusOK, nil)
}

// DeployModule runs the whole tenant path for a tenant that exists: console
// session, upload, wait until validated (at most validateLimit), activation
// with DefaultConfig. It returns the validated module.
func (c *Client) DeployModule(ctx context.Context, tenant, hook, filename string, wasm []byte, validateLimit time.Duration) (Module, error) {
	token, err := c.ConsoleSession(ctx, tenant)
	if err != nil {
		return Module{}, err
	}
	m, err := c.UploadModule(ctx, token, hook, filename, wasm)
	if err != nil {
		return Module{}, err
	}
	m, err = c.WaitValidated(ctx, token, hook, m.ID, validateLimit)
	if err != nil {
		return Module{}, err
	}
	return m, c.Activate(ctx, token, hook, m.ID, DefaultConfig)
}
