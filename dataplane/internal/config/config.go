// Package config models the configuration snapshot the data plane runs on:
// tenants, hook definitions, API keys and the bindings that connect a
// tenant's script to a hook. A snapshot is the unit of change (decision
// D11): the control plane always ships the full state of the world, never a
// delta, so no partial-apply error is possible by construction.
package config

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

// Snapshot is the full configuration of one installation at one version.
// Field names and JSON tags mirror dataplane-internal.openapi.yaml's
// Snapshot schema exactly.
type Snapshot struct {
	Version  int64     `json:"version"`
	APIKeys  []APIKey  `json:"api_keys"`
	Hooks    []HookDef `json:"hooks"`
	Tenants  []Tenant  `json:"tenants"`
	Bindings []Binding `json:"bindings"`
}

// APIKey authenticates an operator against the invoke API. Keys belong to
// the installation (decision D1), not to a tenant.
type APIKey struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"` // lowercase hex of sha256(token)
}

// HookDef is one hook's current definition. The snapshot always carries only
// the current def_version of each hook by name; the executor keeps older
// definitions in memory for as long as a cached module still references
// them.
type HookDef struct {
	Name                 string          `json:"name"`
	DefVersion           int64           `json:"def_version"`
	InputSchema          json.RawMessage `json:"input_schema"`
	OutputSchema         json.RawMessage `json:"output_schema"`
	TimeoutMS            int64           `json:"timeout_ms"`
	MemoryMaxPages       uint32          `json:"memory_max_pages"`
	AllowedHostFunctions []string        `json:"allowed_host_functions"`
	AllowedEffectTypes   []string        `json:"allowed_effect_types"`
	SampleInput          json.RawMessage `json:"sample_input"`
}

// Timeout returns the hook's execution time limit as a time.Duration.
func (h HookDef) Timeout() time.Duration {
	return time.Duration(h.TimeoutMS) * time.Millisecond
}

// SchemaKey returns the schema.Cache key for one of the hook's schemas.
// kind is "input" or "output". The key changes with DefVersion so a new
// hook definition never reuses a stale compiled schema.
func (h HookDef) SchemaKey(kind string) string {
	return fmt.Sprintf("%s@%d/%s", h.Name, h.DefVersion, kind)
}

// Tenant is an operator-defined tenant, identified by ExternalID (decision
// D2).
type Tenant struct {
	ExternalID       string `json:"external_id"`
	ConcurrencyLimit int    `json:"concurrency_limit"` // 0 -> DefaultConcurrencyLimit
	RateLimitRPS     int    `json:"rate_limit_rps"`    // ignored in M0
}

// Binding connects a tenant's script to a hook: which module runs and what
// config it receives.
type Binding struct {
	TenantID      string            `json:"tenant_id"`
	Hook          string            `json:"hook"`
	ModuleHash    string            `json:"module_hash"`
	Config        map[string]string `json:"config"`
	ConfigVersion int64             `json:"config_version"`
}

// DefaultConcurrencyLimit is the concurrency limit a tenant gets when its
// snapshot value is 0.
const DefaultConcurrencyLimit = 4

// moduleHashPattern matches a well-formed module hash. config cannot import
// modstore (package boundaries enforced by archtest), so this duplicates
// modstore's format check rather than depending on it.
var moduleHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// apiKeyHashPattern matches a bare lowercase hex SHA-256 digest, with no
// "sha256:" prefix (APIKey.SHA256 is the hash of a token, not a module
// address).
var apiKeyHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Parse decodes and validates data as a configuration snapshot. Unknown JSON
// fields are ignored (consumers are tolerant readers). A nil Binding.Config
// is normalized to an empty, non-nil map.
func Parse(data []byte) (*Snapshot, error) {
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("config: decode snapshot: %w", err)
	}
	if err := validate(&snap); err != nil {
		return nil, err
	}
	for i := range snap.Bindings {
		if snap.Bindings[i].Config == nil {
			snap.Bindings[i].Config = map[string]string{}
		}
	}
	return &snap, nil
}

func validate(snap *Snapshot) error {
	if snap.Version < 1 {
		return fmt.Errorf("config: version %d must be >= 1", snap.Version)
	}

	hookNames := make(map[string]bool, len(snap.Hooks))
	for _, h := range snap.Hooks {
		if hookNames[h.Name] {
			return fmt.Errorf("config: duplicate hook %q", h.Name)
		}
		hookNames[h.Name] = true

		if h.DefVersion < 1 {
			return fmt.Errorf("config: hook %q: def_version %d must be >= 1", h.Name, h.DefVersion)
		}
		if h.TimeoutMS <= 0 {
			return fmt.Errorf("config: hook %q: timeout_ms %d must be > 0", h.Name, h.TimeoutMS)
		}
		if h.MemoryMaxPages == 0 {
			return fmt.Errorf("config: hook %q: memory_max_pages must be > 0", h.Name)
		}
		if !isJSONObject(h.InputSchema) {
			return fmt.Errorf("config: hook %q: input_schema must be a JSON object", h.Name)
		}
		if !isJSONObject(h.OutputSchema) {
			return fmt.Errorf("config: hook %q: output_schema must be a JSON object", h.Name)
		}
	}

	tenantIDs := make(map[string]bool, len(snap.Tenants))
	for _, t := range snap.Tenants {
		if tenantIDs[t.ExternalID] {
			return fmt.Errorf("config: duplicate tenant %q", t.ExternalID)
		}
		tenantIDs[t.ExternalID] = true

		if t.ConcurrencyLimit < 0 {
			return fmt.Errorf("config: tenant %q: concurrency_limit %d must be >= 0", t.ExternalID, t.ConcurrencyLimit)
		}
	}

	type bindingKey struct{ tenantID, hook string }
	bindingKeys := make(map[bindingKey]bool, len(snap.Bindings))
	for _, b := range snap.Bindings {
		key := bindingKey{b.TenantID, b.Hook}
		if bindingKeys[key] {
			return fmt.Errorf("config: duplicate binding for tenant %q hook %q", b.TenantID, b.Hook)
		}
		bindingKeys[key] = true

		if !moduleHashPattern.MatchString(b.ModuleHash) {
			return fmt.Errorf("config: binding tenant %q hook %q: malformed module_hash %q", b.TenantID, b.Hook, b.ModuleHash)
		}
		if !hookNames[b.Hook] {
			return fmt.Errorf("config: binding tenant %q: references unknown hook %q", b.TenantID, b.Hook)
		}
		if !tenantIDs[b.TenantID] {
			return fmt.Errorf("config: binding hook %q: references unknown tenant %q", b.Hook, b.TenantID)
		}
	}

	for _, k := range snap.APIKeys {
		if !apiKeyHashPattern.MatchString(k.SHA256) {
			return fmt.Errorf("config: api key %q: sha256 must be 64 lowercase hex characters", k.ID)
		}
	}

	return nil
}

// isJSONObject reports whether raw is present and its first non-whitespace
// byte opens a JSON object. By the time this runs, data has already been
// through json.Unmarshal successfully, so a present raw is guaranteed
// syntactically valid JSON of some type; this only narrows that down to
// "object".
func isJSONObject(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

// View is a read-optimized, immutable index over a Snapshot. Build a new
// View for each Snapshot rather than mutating one in place.
type View struct {
	snap     *Snapshot
	hooks    map[string]HookDef
	tenants  map[string]Tenant
	bindings map[viewBindingKey]Binding
}

type viewBindingKey struct{ tenantID, hook string }

// NewView indexes s for lookup. s is assumed already validated (e.g. by
// Parse); NewView does not re-validate it.
func NewView(s *Snapshot) *View {
	v := &View{
		snap:     s,
		hooks:    make(map[string]HookDef, len(s.Hooks)),
		tenants:  make(map[string]Tenant, len(s.Tenants)),
		bindings: make(map[viewBindingKey]Binding, len(s.Bindings)),
	}
	for _, h := range s.Hooks {
		v.hooks[h.Name] = h
	}
	for _, t := range s.Tenants {
		v.tenants[t.ExternalID] = t
	}
	for _, b := range s.Bindings {
		v.bindings[viewBindingKey{b.TenantID, b.Hook}] = b
	}
	return v
}

// Version returns the snapshot's version.
func (v *View) Version() int64 { return v.snap.Version }

// Hook looks up a hook definition by name.
func (v *View) Hook(name string) (HookDef, bool) {
	h, ok := v.hooks[name]
	return h, ok
}

// Tenant looks up a tenant by external ID. A zero ConcurrencyLimit in the
// snapshot is normalized to DefaultConcurrencyLimit here.
func (v *View) Tenant(id string) (Tenant, bool) {
	t, ok := v.tenants[id]
	if !ok {
		return Tenant{}, false
	}
	if t.ConcurrencyLimit == 0 {
		t.ConcurrencyLimit = DefaultConcurrencyLimit
	}
	return t, true
}

// Binding looks up the binding for a (tenant, hook) pair.
func (v *View) Binding(tenantID, hook string) (Binding, bool) {
	b, ok := v.bindings[viewBindingKey{tenantID, hook}]
	return b, ok
}

// Bindings returns all bindings in the snapshot. The returned slice is a
// copy; mutating it does not affect the View.
func (v *View) Bindings() []Binding {
	out := make([]Binding, len(v.snap.Bindings))
	copy(out, v.snap.Bindings)
	return out
}

// AuthenticateToken reports whether token's SHA-256 matches one of the
// snapshot's API keys. Comparison is constant-time per key to avoid timing
// side channels; the empty token never authenticates even if some key's
// hash were (implausibly) empty or all-zero.
func (v *View) AuthenticateToken(token string) bool {
	if token == "" {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	got := hex.EncodeToString(sum[:])
	ok := false
	for _, k := range v.snap.APIKeys {
		if subtle.ConstantTimeCompare([]byte(got), []byte(k.SHA256)) == 1 {
			ok = true
		}
	}
	return ok
}
