// Package httpapi serves the public data plane API defined in
// api/invoke.openapi.yaml: invoke, liveness and readiness.
//
// Status codes follow spec M0 §6.1.3: 200 for every outcome where the
// platform did its job (ok, no_handler, timeout, handler_error), 429 for
// quota_exceeded, 503 for unavailable, RFC 9457 problem details for request
// errors. X-Hook-Outcome repeats the outcome for proxies and metrics.
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/gateway"
)

// OutcomeHeader carries the outcome of an invocation.
const OutcomeHeader = "X-Hook-Outcome"

// Options tune the handler. Zero values take the defaults.
type Options struct {
	MaxBodyBytes int64       // default 1 MiB
	Ready        func() bool // nil: always ready
}

// New returns the HTTP handler of the public API.
func New(g *gateway.Gateway, opts Options) http.Handler {
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 1 << 20
	}
	if opts.Ready == nil {
		opts.Ready = func() bool { return true }
	}
	h := &handler{g: g, opts: opts}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/hooks/{hook}/invoke", h.invoke)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", h.readyz)
	return mux
}

type handler struct {
	g    *gateway.Gateway
	opts Options
}

type invokeRequest struct {
	TenantID       string          `json:"tenant_id"`
	Payload        json.RawMessage `json:"payload"`
	DeadlineMS     *int64          `json:"deadline_ms"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type effect struct {
	Type    string          `json:"type"`
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload"`
}

type invokeResponse struct {
	Outcome    execproto.Outcome `json:"outcome"`
	Result     json.RawMessage   `json:"result"`
	Effects    []effect          `json:"effects"`
	Error      *string           `json:"error"`
	DurationMS int64             `json:"duration_ms"`
	ModuleHash *string           `json:"module_hash"`
}

const maxDeadlineMS = 30000

func (h *handler) invoke(w http.ResponseWriter, r *http.Request) {
	if !h.g.Authenticate(bearerToken(r)) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="wasmhooks"`)
		problem(w, http.StatusUnauthorized, "missing or invalid API key")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.opts.MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			problem(w, http.StatusRequestEntityTooLarge, "request body exceeds the size limit")
			return
		}
		problem(w, http.StatusBadRequest, "cannot read request body")
		return
	}
	var req invokeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		problem(w, http.StatusBadRequest, "request body is not a valid JSON object: "+err.Error())
		return
	}
	switch {
	case strings.TrimSpace(req.TenantID) == "":
		problem(w, http.StatusBadRequest, "tenant_id is required")
		return
	case !isObject(req.Payload):
		problem(w, http.StatusBadRequest, "payload is required and must be a JSON object")
		return
	case req.DeadlineMS != nil && *req.DeadlineMS < 1:
		problem(w, http.StatusBadRequest, "deadline_ms must be at least 1")
		return
	}
	var deadline time.Duration
	if req.DeadlineMS != nil {
		deadline = time.Duration(min(*req.DeadlineMS, maxDeadlineMS)) * time.Millisecond
	}

	resp, err := h.g.Invoke(r.Context(), gateway.Request{
		Hook:           r.PathValue("hook"),
		TenantID:       req.TenantID,
		Payload:        req.Payload,
		Deadline:       deadline,
		IdempotencyKey: req.IdempotencyKey,
	})
	switch {
	case errors.Is(err, gateway.ErrUnknownHook):
		problem(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, gateway.ErrInvalidPayload):
		problem(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		// Not ready, or a broken hook definition: the platform could not
		// serve the request. The contract has no 500 for invoke.
		resp = gateway.Response{
			Outcome: execproto.OutcomeUnavailable,
			Reason:  execproto.ReasonInternal,
			Error:   err.Error(),
		}
	}
	writeOutcome(w, resp)
}

func writeOutcome(w http.ResponseWriter, resp gateway.Response) {
	out := invokeResponse{
		Outcome:    resp.Outcome,
		Effects:    []effect{},
		DurationMS: resp.Duration.Milliseconds(),
	}
	if resp.Outcome == execproto.OutcomeOK {
		out.Result = resp.Result
		for _, e := range resp.Effects {
			out.Effects = append(out.Effects, effect{Type: e.Type, Key: e.Key, Payload: e.Payload})
		}
	}
	if resp.Error != "" && (resp.Outcome == execproto.OutcomeHandlerError || resp.Outcome == execproto.OutcomeUnavailable) {
		out.Error = &resp.Error
	}
	if resp.ModuleHash != "" {
		out.ModuleHash = &resp.ModuleHash
	}
	if out.Result == nil {
		out.Result = json.RawMessage("null")
	}

	status := http.StatusOK
	switch resp.Outcome {
	case execproto.OutcomeQuotaExceeded:
		status = http.StatusTooManyRequests
	case execproto.OutcomeUnavailable:
		status = http.StatusServiceUnavailable
	}
	w.Header().Set(OutcomeHeader, string(resp.Outcome))
	writeJSON(w, status, "application/json", out)
}

func (h *handler) readyz(w http.ResponseWriter, _ *http.Request) {
	if h.opts.Ready() {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}

type problemDetails struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func problem(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, "application/problem+json", problemDetails{
		Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail,
	})
}

func writeJSON(w http.ResponseWriter, status int, contentType string, v any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func bearerToken(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func isObject(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{'
}
