package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// validSnapshotJSON mirrors the example in the M0 design spec section 6.2
// and the dataplane-internal.openapi.yaml Snapshot schema, field for field.
const validSnapshotJSON = `{
	"version": 42,
	"api_keys": [
		{"id": "key_01", "name": "shop-prod", "sha256": "dc00398b71610ed73135aa53cb1d4e75031a8d5386c066f528cb176173422bbb"}
	],
	"hooks": [
		{
			"name": "checkout.discount",
			"def_version": 3,
			"input_schema": {"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"},
			"output_schema": {"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"},
			"timeout_ms": 50,
			"memory_max_pages": 256,
			"allowed_host_functions": [],
			"allowed_effect_types": [],
			"sample_input": {"customer": {"lifetime_spend": 1500}}
		}
	],
	"tenants": [
		{"external_id": "merchant-a", "concurrency_limit": 4, "rate_limit_rps": 100}
	],
	"bindings": [
		{
			"tenant_id": "merchant-a",
			"hook": "checkout.discount",
			"module_hash": "sha256:1230c6a675e495a7bdcbc7695fe1344c0acd799d2dd07c1a2e01a071dd4ec724",
			"config": {"threshold": "1000", "percent": "10"},
			"config_version": 7
		}
	]
}`

func TestParse_ValidSnapshot(t *testing.T) {
	snap, err := Parse([]byte(validSnapshotJSON))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if snap.Version != 42 {
		t.Errorf("Version = %d, want 42", snap.Version)
	}
	if len(snap.APIKeys) != 1 || snap.APIKeys[0].ID != "key_01" {
		t.Errorf("APIKeys = %+v", snap.APIKeys)
	}
	if len(snap.Hooks) != 1 || snap.Hooks[0].Name != "checkout.discount" {
		t.Errorf("Hooks = %+v", snap.Hooks)
	}
	if len(snap.Tenants) != 1 || snap.Tenants[0].ExternalID != "merchant-a" {
		t.Errorf("Tenants = %+v", snap.Tenants)
	}
	if len(snap.Bindings) != 1 || snap.Bindings[0].ModuleHash != "sha256:1230c6a675e495a7bdcbc7695fe1344c0acd799d2dd07c1a2e01a071dd4ec724" {
		t.Errorf("Bindings = %+v", snap.Bindings)
	}
}

func TestParse_UnknownFieldsIgnored(t *testing.T) {
	data := replaceOnce(t, validSnapshotJSON, `"version": 42,`, `"version": 42, "unknown_field": "surprise",`)
	if _, err := Parse([]byte(data)); err != nil {
		t.Fatalf("Parse() should ignore unknown fields, got error: %v", err)
	}
}

func TestParse_NilBindingConfigNormalizesToEmptyMap(t *testing.T) {
	data := replaceOnce(t, validSnapshotJSON, `"config": {"threshold": "1000", "percent": "10"},`, `"config": null,`)
	snap, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if snap.Bindings[0].Config == nil {
		t.Fatal("Bindings[0].Config is nil, want an empty non-nil map")
	}
	if len(snap.Bindings[0].Config) != 0 {
		t.Fatalf("Bindings[0].Config = %+v, want empty", snap.Bindings[0].Config)
	}
}

func TestHookDef_Timeout(t *testing.T) {
	h := HookDef{TimeoutMS: 50}
	if got, want := h.Timeout(), 50*time.Millisecond; got != want {
		t.Fatalf("Timeout() = %v, want %v", got, want)
	}
}

func TestHookDef_SchemaKey(t *testing.T) {
	h := HookDef{Name: "checkout.discount", DefVersion: 3}
	if got, want := h.SchemaKey("input"), "checkout.discount@3/input"; got != want {
		t.Fatalf("SchemaKey(input) = %q, want %q", got, want)
	}
	if got, want := h.SchemaKey("output"), "checkout.discount@3/output"; got != want {
		t.Fatalf("SchemaKey(output) = %q, want %q", got, want)
	}
}

// replaceOnce builds an invalid variant of doc by replacing old with new. It
// fails the test immediately if old doesn't occur in doc exactly once, so a
// broken fixture is caught loudly instead of silently testing the happy
// path (0 occurrences) or an ambiguous edit (2+ occurrences).
func replaceOnce(t *testing.T, doc, old, new string) string {
	t.Helper()
	if n := strings.Count(doc, old); n != 1 {
		t.Fatalf("fixture setup: %q occurs %d times in doc, want 1", old, n)
	}
	return strings.Replace(doc, old, new, 1)
}

const secondHookJSON = `,
		{
			"name": "checkout.discount",
			"def_version": 4,
			"input_schema": {"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"},
			"output_schema": {"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"},
			"timeout_ms": 50,
			"memory_max_pages": 256,
			"allowed_host_functions": [],
			"allowed_effect_types": [],
			"sample_input": {}
		}`

const secondTenantJSON = `,
		{"external_id": "merchant-a", "concurrency_limit": 2, "rate_limit_rps": 50}`

const secondBindingJSON = `,
		{
			"tenant_id": "merchant-a",
			"hook": "checkout.discount",
			"module_hash": "sha256:1230c6a675e495a7bdcbc7695fe1344c0acd799d2dd07c1a2e01a071dd4ec724",
			"config": {},
			"config_version": 8
		}`

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantSub string
	}{
		{
			name:    "version below 1",
			doc:     replaceOnce(t, validSnapshotJSON, `"version": 42,`, `"version": 0,`),
			wantSub: "version",
		},
		{
			name: "duplicate hook name",
			doc: replaceOnce(t, validSnapshotJSON,
				`"sample_input": {"customer": {"lifetime_spend": 1500}}
		}
	],`,
				`"sample_input": {"customer": {"lifetime_spend": 1500}}
		}`+secondHookJSON+`
	],`),
			wantSub: "duplicate hook",
		},
		{
			name: "duplicate tenant external_id",
			doc: replaceOnce(t, validSnapshotJSON,
				`{"external_id": "merchant-a", "concurrency_limit": 4, "rate_limit_rps": 100}
	],`,
				`{"external_id": "merchant-a", "concurrency_limit": 4, "rate_limit_rps": 100}`+secondTenantJSON+`
	],`),
			wantSub: "duplicate tenant",
		},
		{
			name: "duplicate binding (tenant_id, hook)",
			doc: replaceOnce(t, validSnapshotJSON,
				`"config_version": 7
		}
	]`,
				`"config_version": 7
		}`+secondBindingJSON+`
	]`),
			wantSub: "duplicate binding",
		},
		{
			name:    "binding references unknown hook",
			doc:     replaceOnce(t, validSnapshotJSON, `"hook": "checkout.discount",`, `"hook": "checkout.does_not_exist",`),
			wantSub: "unknown hook",
		},
		{
			name:    "binding references unknown tenant",
			doc:     replaceOnce(t, validSnapshotJSON, `"tenant_id": "merchant-a",`, `"tenant_id": "merchant-ghost",`),
			wantSub: "unknown tenant",
		},
		{
			name:    "module_hash wrong format",
			doc:     replaceOnce(t, validSnapshotJSON, `"module_hash": "sha256:1230c6a675e495a7bdcbc7695fe1344c0acd799d2dd07c1a2e01a071dd4ec724",`, `"module_hash": "sha256:not-hex",`),
			wantSub: "module_hash",
		},
		{
			name:    "timeout_ms zero",
			doc:     replaceOnce(t, validSnapshotJSON, `"timeout_ms": 50,`, `"timeout_ms": 0,`),
			wantSub: "timeout_ms",
		},
		{
			name:    "timeout_ms negative",
			doc:     replaceOnce(t, validSnapshotJSON, `"timeout_ms": 50,`, `"timeout_ms": -1,`),
			wantSub: "timeout_ms",
		},
		{
			name:    "memory_max_pages zero",
			doc:     replaceOnce(t, validSnapshotJSON, `"memory_max_pages": 256,`, `"memory_max_pages": 0,`),
			wantSub: "memory_max_pages",
		},
		{
			name:    "timeout_ms above 30000",
			doc:     replaceOnce(t, validSnapshotJSON, `"timeout_ms": 50,`, `"timeout_ms": 30001,`),
			wantSub: "timeout_ms",
		},
		{
			name:    "memory_max_pages below the Extism kernel minimum",
			doc:     replaceOnce(t, validSnapshotJSON, `"memory_max_pages": 256,`, `"memory_max_pages": 15,`),
			wantSub: "memory_max_pages",
		},
		{
			name:    "memory_max_pages above 16384",
			doc:     replaceOnce(t, validSnapshotJSON, `"memory_max_pages": 256,`, `"memory_max_pages": 16385,`),
			wantSub: "memory_max_pages",
		},
		{
			name:    "def_version below 1",
			doc:     replaceOnce(t, validSnapshotJSON, `"def_version": 3,`, `"def_version": 0,`),
			wantSub: "def_version",
		},
		{
			name:    "input_schema missing",
			doc:     replaceOnce(t, validSnapshotJSON, `"input_schema": {"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"},`, ``),
			wantSub: "input_schema",
		},
		{
			name:    "input_schema not a JSON object",
			doc:     replaceOnce(t, validSnapshotJSON, `"input_schema": {"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"},`, `"input_schema": [1, 2, 3],`),
			wantSub: "input_schema",
		},
		{
			name:    "output_schema missing",
			doc:     replaceOnce(t, validSnapshotJSON, `"output_schema": {"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"},`, ``),
			wantSub: "output_schema",
		},
		{
			name:    "concurrency_limit negative",
			doc:     replaceOnce(t, validSnapshotJSON, `"concurrency_limit": 4,`, `"concurrency_limit": -1,`),
			wantSub: "concurrency_limit",
		},
		{
			name:    "api key sha256 not 64 hex",
			doc:     replaceOnce(t, validSnapshotJSON, `"sha256": "dc00398b71610ed73135aa53cb1d4e75031a8d5386c066f528cb176173422bbb"`, `"sha256": "not-a-valid-hash"`),
			wantSub: "sha256",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.doc))
			if err == nil {
				t.Fatalf("Parse() should fail for %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("Parse() error = %q, want substring %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestParse_MalformedJSON(t *testing.T) {
	if _, err := Parse([]byte(`not json`)); err == nil {
		t.Fatal("Parse() should fail on malformed JSON")
	}
}

// TestFixtureSanity guards the fixture itself: if validSnapshotJSON stops
// being valid JSON, every replaceOnce-derived error case above would pass
// for the wrong reason (malformed JSON) instead of the intended validation
// rule.
func TestFixtureSanity(t *testing.T) {
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(validSnapshotJSON), &raw); err != nil {
		t.Fatalf("validSnapshotJSON is not valid JSON: %v", err)
	}
}

func mustParse(t *testing.T, doc string) *Snapshot {
	t.Helper()
	snap, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse(): %v", err)
	}
	return snap
}

func TestView_Hook(t *testing.T) {
	v := NewView(mustParse(t, validSnapshotJSON))

	h, ok := v.Hook("checkout.discount")
	if !ok {
		t.Fatal("Hook(checkout.discount) not found")
	}
	if h.DefVersion != 3 {
		t.Fatalf("Hook().DefVersion = %d, want 3", h.DefVersion)
	}

	if _, ok := v.Hook("does.not.exist"); ok {
		t.Fatal("Hook(does.not.exist) should not be found")
	}
}

func TestView_Tenant(t *testing.T) {
	v := NewView(mustParse(t, validSnapshotJSON))

	tenant, ok := v.Tenant("merchant-a")
	if !ok {
		t.Fatal("Tenant(merchant-a) not found")
	}
	if tenant.ConcurrencyLimit != 4 {
		t.Fatalf("Tenant().ConcurrencyLimit = %d, want 4", tenant.ConcurrencyLimit)
	}

	if _, ok := v.Tenant("merchant-ghost"); ok {
		t.Fatal("Tenant(merchant-ghost) should not be found")
	}
}

func TestView_Tenant_ZeroConcurrencyLimitDefaults(t *testing.T) {
	doc := replaceOnce(t, validSnapshotJSON, `"concurrency_limit": 4,`, `"concurrency_limit": 0,`)
	v := NewView(mustParse(t, doc))

	tenant, ok := v.Tenant("merchant-a")
	if !ok {
		t.Fatal("Tenant(merchant-a) not found")
	}
	if tenant.ConcurrencyLimit != DefaultConcurrencyLimit {
		t.Fatalf("Tenant().ConcurrencyLimit = %d, want default %d", tenant.ConcurrencyLimit, DefaultConcurrencyLimit)
	}
}

func TestView_Binding(t *testing.T) {
	v := NewView(mustParse(t, validSnapshotJSON))

	b, ok := v.Binding("merchant-a", "checkout.discount")
	if !ok {
		t.Fatal("Binding(merchant-a, checkout.discount) not found")
	}
	if b.ConfigVersion != 7 {
		t.Fatalf("Binding().ConfigVersion = %d, want 7", b.ConfigVersion)
	}

	if _, ok := v.Binding("merchant-a", "no.such.hook"); ok {
		t.Fatal("Binding(merchant-a, no.such.hook) should not be found")
	}
}

func TestView_Bindings(t *testing.T) {
	v := NewView(mustParse(t, validSnapshotJSON))
	bindings := v.Bindings()
	if len(bindings) != 1 {
		t.Fatalf("Bindings() = %+v, want 1 entry", bindings)
	}
	bindings[0].Hook = "mutated"
	if b, _ := v.Binding("merchant-a", "checkout.discount"); b.Hook != "checkout.discount" {
		t.Fatal("mutating the slice returned by Bindings() must not affect the View")
	}
}

func TestView_Version(t *testing.T) {
	v := NewView(mustParse(t, validSnapshotJSON))
	if v.Version() != 42 {
		t.Fatalf("Version() = %d, want 42", v.Version())
	}
}

func TestView_AuthenticateToken(t *testing.T) {
	// sha256("correct-horse-battery-staple")
	const token = "correct-horse-battery-staple"
	sum := sha256.Sum256([]byte(token))
	doc := replaceOnce(t, validSnapshotJSON,
		`"sha256": "dc00398b71610ed73135aa53cb1d4e75031a8d5386c066f528cb176173422bbb"`,
		fmt.Sprintf(`"sha256": %q`, hex.EncodeToString(sum[:])))
	v := NewView(mustParse(t, doc))

	if !v.AuthenticateToken(token) {
		t.Fatal("AuthenticateToken(correct token) = false, want true")
	}
	if v.AuthenticateToken("wrong-token") {
		t.Fatal("AuthenticateToken(wrong token) = true, want false")
	}
	if v.AuthenticateToken("") {
		t.Fatal("AuthenticateToken(\"\") = true, want false")
	}
}
