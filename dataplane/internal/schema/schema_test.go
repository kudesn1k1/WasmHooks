package schema

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// cartTotalSchema requires an object with a numeric cart_total, matching the
// brief's example schema.
const cartTotalSchema = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"type": "object",
	"required": ["cart_total"],
	"properties": {
		"cart_total": {"type": "number"}
	}
}`

func TestCompile(t *testing.T) {
	t.Run("valid schema compiles", func(t *testing.T) {
		if _, err := Compile(json.RawMessage(cartTotalSchema)); err != nil {
			t.Fatalf("Compile() error: %v", err)
		}
	})

	t.Run("empty schema accepts everything", func(t *testing.T) {
		s, err := Compile(json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("Compile(): %v", err)
		}
		if err := s.Validate([]byte(`{"anything": [1, 2, 3], "n": 1.5}`)); err != nil {
			t.Fatalf("Validate() with permissive schema: %v", err)
		}
		if err := s.Validate([]byte(`42`)); err != nil {
			t.Fatalf("Validate() with permissive schema on scalar: %v", err)
		}
	})

	t.Run("invalid schema fails to compile", func(t *testing.T) {
		_, err := Compile(json.RawMessage(`{"type": 5}`))
		if err == nil {
			t.Fatal("Compile() with {\"type\": 5} should fail")
		}
		var ve *ValidationError
		if errors.As(err, &ve) {
			t.Fatalf("Compile() error should not be a *ValidationError, got %v", err)
		}
	})

	t.Run("malformed JSON fails to compile", func(t *testing.T) {
		if _, err := Compile(json.RawMessage(`not json`)); err == nil {
			t.Fatal("Compile() with non-JSON should fail")
		}
	})
}

func TestSchema_Validate(t *testing.T) {
	s, err := Compile(json.RawMessage(cartTotalSchema))
	if err != nil {
		t.Fatalf("Compile(): %v", err)
	}

	t.Run("valid document", func(t *testing.T) {
		if err := s.Validate([]byte(`{"cart_total": 10}`)); err != nil {
			t.Fatalf("Validate() error: %v", err)
		}
	})

	t.Run("missing required field", func(t *testing.T) {
		err := s.Validate([]byte(`{}`))
		if err == nil {
			t.Fatal("Validate() should fail on missing cart_total")
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("Validate() error type = %T, want *ValidationError", err)
		}
		if !strings.Contains(ve.Detail, "cart_total") {
			t.Fatalf("ValidationError.Detail = %q, want it to mention cart_total", ve.Detail)
		}
		if ve.Error() != ve.Detail {
			t.Fatalf("Error() = %q, want it to equal Detail %q", ve.Error(), ve.Detail)
		}
	})

	t.Run("document is not JSON", func(t *testing.T) {
		err := s.Validate([]byte(`not json`))
		if err == nil {
			t.Fatal("Validate() should fail on non-JSON document")
		}
		var ve *ValidationError
		if errors.As(err, &ve) {
			t.Fatalf("Validate() on non-JSON document should not be a *ValidationError, got %v", err)
		}
	})

	t.Run("Detail is deterministic across repeated failures", func(t *testing.T) {
		var first string
		for i := 0; i < 5; i++ {
			err := s.Validate([]byte(`{}`))
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("run %d: error type = %T, want *ValidationError", i, err)
			}
			if i == 0 {
				first = ve.Detail
			} else if ve.Detail != first {
				t.Fatalf("run %d: Detail = %q, want %q (deterministic)", i, ve.Detail, first)
			}
		}
	})

	// Regression test: jsonschema evaluates "properties" by ranging over a
	// Go map, so a document that fails several sibling properties at once
	// produces Causes in an order that is NOT stable across calls. detailOf
	// must not simply follow Causes[0], or Detail would flap between "/a"
	// and "/b" from one Validate call to the next on the exact same input.
	t.Run("Detail is deterministic when multiple properties fail", func(t *testing.T) {
		multi, err := Compile(json.RawMessage(`{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type": "object",
			"properties": {
				"a": {"type": "number"},
				"b": {"type": "number"},
				"c": {"type": "number"},
				"d": {"type": "number"}
			}
		}`))
		if err != nil {
			t.Fatalf("Compile(): %v", err)
		}
		doc := []byte(`{"a":"x","b":"y","c":"z","d":"w"}`)

		var first string
		for i := 0; i < 200; i++ {
			err := multi.Validate(doc)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("run %d: error type = %T, want *ValidationError", i, err)
			}
			if i == 0 {
				first = ve.Detail
			} else if ve.Detail != first {
				t.Fatalf("run %d: Detail = %q, want %q (deterministic)", i, ve.Detail, first)
			}
		}
		if !strings.Contains(first, "/a") {
			t.Fatalf("Detail = %q, want the lexicographically first pointer /a", first)
		}
	})
}

// TestSchema_Validate_BigNumberPrecision guards against the common bug of
// decoding instance documents through encoding/json into interface{}, which
// collapses large integers into float64 and silently loses precision. Two
// numbers that differ only in their last significant digit must be told
// apart.
func TestSchema_Validate_BigNumberPrecision(t *testing.T) {
	const raw = `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"n": {"const": 123456789012345678901234567890}
		}
	}`
	s, err := Compile(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Compile(): %v", err)
	}

	if err := s.Validate([]byte(`{"n": 123456789012345678901234567890}`)); err != nil {
		t.Fatalf("Validate() on the exact const value: %v", err)
	}
	// Differs only in the last digit. A float64 round trip would conflate
	// this with the value above.
	if err := s.Validate([]byte(`{"n": 123456789012345678901234567891}`)); err == nil {
		t.Fatal("Validate() should reject a value differing in its last digit from const")
	}
}

func TestCache(t *testing.T) {
	t.Run("compiles once per key", func(t *testing.T) {
		var calls atomic.Int32
		orig := compileFn
		compileFn = func(raw json.RawMessage) (*Schema, error) {
			calls.Add(1)
			return orig(raw)
		}
		defer func() { compileFn = orig }()

		c := NewCache()
		raw := json.RawMessage(cartTotalSchema)
		for i := 0; i < 10; i++ {
			if _, err := c.Get("checkout.discount@1/input", raw); err != nil {
				t.Fatalf("Get() call %d: %v", i, err)
			}
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("compileFn called %d times, want 1", got)
		}
	})

	t.Run("different keys compile independently", func(t *testing.T) {
		var calls atomic.Int32
		orig := compileFn
		compileFn = func(raw json.RawMessage) (*Schema, error) {
			calls.Add(1)
			return orig(raw)
		}
		defer func() { compileFn = orig }()

		c := NewCache()
		raw := json.RawMessage(cartTotalSchema)
		if _, err := c.Get("a", raw); err != nil {
			t.Fatalf("Get(a): %v", err)
		}
		if _, err := c.Get("b", raw); err != nil {
			t.Fatalf("Get(b): %v", err)
		}
		if got := calls.Load(); got != 2 {
			t.Fatalf("compileFn called %d times, want 2", got)
		}
	})

	t.Run("concurrent Get is safe and still compiles once", func(t *testing.T) {
		var calls atomic.Int32
		orig := compileFn
		compileFn = func(raw json.RawMessage) (*Schema, error) {
			calls.Add(1)
			return orig(raw)
		}
		defer func() { compileFn = orig }()

		c := NewCache()
		raw := json.RawMessage(cartTotalSchema)
		var wg sync.WaitGroup
		for range 50 {
			wg.Go(func() {
				if _, err := c.Get("checkout.discount@1/input", raw); err != nil {
					t.Errorf("Get(): %v", err)
				}
			})
		}
		wg.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("compileFn called %d times, want 1", got)
		}
	})

	t.Run("returned schema validates", func(t *testing.T) {
		c := NewCache()
		s, err := c.Get("k", json.RawMessage(cartTotalSchema))
		if err != nil {
			t.Fatalf("Get(): %v", err)
		}
		if err := s.Validate([]byte(`{}`)); err == nil {
			t.Fatal("expected validation failure on missing cart_total")
		}
	})

	t.Run("compile error is cached too", func(t *testing.T) {
		c := NewCache()
		_, err1 := c.Get("bad", json.RawMessage(`{"type": 5}`))
		if err1 == nil {
			t.Fatal("Get() should surface the compile error")
		}
		_, err2 := c.Get("bad", json.RawMessage(`{"type": 5}`))
		if err2 == nil || err2.Error() != err1.Error() {
			t.Fatalf("Get() second call error = %v, want same as first %v", err2, err1)
		}
	})
}
