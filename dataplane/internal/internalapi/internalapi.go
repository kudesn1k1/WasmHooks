// Package internalapi serves the data plane's internal API: module
// validation for the control plane (POST /internal/v1/modules/validate,
// api/dataplane-internal.openapi.yaml). It listens on its own port, which an
// installation never publishes, and every request needs the internal token
// shared by the two planes.
package internalapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/executor"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
)

// maxBodyBytes caps a validation request: a hook definition with schemas
// and a sample input.
const maxBodyBytes = 1 << 20

// Validator is what the API needs from the executor.
type Validator interface {
	Validate(ctx context.Context, req executor.ValidateRequest) (executor.ValidationReport, error)
}

type handler struct {
	v     Validator
	token []byte
	log   *slog.Logger
}

// New serves the internal API behind token.
func New(v Validator, token string, log *slog.Logger) http.Handler {
	h := &handler{v: v, token: []byte(token), log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/v1/modules/validate", h.validate)
	return h.authenticated(mux)
}

func (h *handler) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare([]byte(token), h.token) != 1 {
			problem(w, http.StatusUnauthorized, "missing or invalid internal token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type validateRequest struct {
	ModuleHash string            `json:"module_hash"`
	Hook       *config.HookDef   `json:"hook"`
	Config     map[string]string `json:"config"`
}

func (h *handler) validate(w http.ResponseWriter, r *http.Request) {
	var req validateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			problem(w, http.StatusRequestEntityTooLarge, "request body exceeds the size limit")
			return
		}
		problem(w, http.StatusBadRequest, "request body is not valid JSON: "+err.Error())
		return
	}
	if _, err := modstore.ParseHash(req.ModuleHash); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	switch {
	case req.Hook == nil:
		problem(w, http.StatusBadRequest, "hook is required")
		return
	case strings.TrimSpace(req.Hook.Name) == "":
		problem(w, http.StatusBadRequest, "hook.name is required")
		return
	case len(req.Hook.SampleInput) == 0:
		problem(w, http.StatusBadRequest, "hook.sample_input is required")
		return
	}
	if err := req.Hook.Validate(); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}

	report, err := h.v.Validate(r.Context(), executor.ValidateRequest{
		ModuleHash: req.ModuleHash, Hook: *req.Hook, Config: req.Config,
	})
	if errors.Is(err, executor.ErrInvalidHook) {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	if errors.Is(err, executor.ErrUnavailable) {
		h.log.Warn("module validation unavailable", "module_hash", req.ModuleHash, "err", err)
		problem(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		h.log.Error("module validation failed", "module_hash", req.ModuleHash, "err", err)
		problem(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, "application/json", report)
}

// The same problem-details shape as gateway/httpapi; the packages stay
// independent, so the dozen lines are repeated rather than shared.
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
