// Package schema validates JSON documents against per-hook JSON Schemas
// (draft 2020-12). Hook definitions carry their input and output schemas as
// raw JSON; the executor compiles each one once and reuses it for every
// call.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// msgPrinter renders jsonschema's ErrorKind messages as plain English. The
// library has no locale-independent accessor, so a fixed locale is the only
// way to get stable, deterministic text out of it.
var msgPrinter = message.NewPrinter(language.English)

// schemaLoc is the resource URL each compile uses. A new *jsonschema.Compiler
// is created per Compile call, so this constant is never shared across
// schemas and cannot collide.
const schemaLoc = "mem://schema.json"

// Schema is a compiled JSON Schema.
type Schema struct {
	compiled *jsonschema.Schema
}

// ValidationError reports that a document failed schema validation. Detail
// is one validation failure, chosen deterministically (see detailOf) so it
// is stable for a given schema and document across runs, formatted as
// "<json pointer>: <message>".
type ValidationError struct {
	Detail string
}

// Error implements error. It equals Detail.
func (e *ValidationError) Error() string { return e.Detail }

// Compile compiles a JSON Schema document. It defaults to draft 2020-12 when
// the document has no "$schema" keyword. It returns an error if raw is not
// valid JSON or not a valid schema.
func Compile(raw json.RawMessage) (*Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("schema: decode schema document: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(schemaLoc, doc); err != nil {
		return nil, fmt.Errorf("schema: add schema resource: %w", err)
	}
	compiled, err := c.Compile(schemaLoc)
	if err != nil {
		return nil, fmt.Errorf("schema: compile schema: %w", err)
	}
	return &Schema{compiled: compiled}, nil
}

// Validate checks doc against s. It returns a *ValidationError if doc
// doesn't match the schema, or a different error if doc is not valid JSON.
func (s *Schema) Validate(doc []byte) error {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return fmt.Errorf("schema: document is not valid JSON: %w", err)
	}
	if err := s.compiled.Validate(inst); err != nil {
		ve, ok := err.(*jsonschema.ValidationError)
		if !ok {
			return fmt.Errorf("schema: validate: %w", err)
		}
		return &ValidationError{Detail: detailOf(ve)}
	}
	return nil
}

// detailOf picks one leaf validation failure and renders it as
// "<json pointer>: <message>". A *jsonschema.ValidationError is a tree: a
// keyword like "properties" can fail with one Cause per invalid property,
// and internally those causes come from ranging over a Go map, so their
// order is not stable across calls. Always following Causes[0] would
// therefore make Detail flap from one Validate call to the next on the same
// input. Instead, every leaf is collected and sorted by (pointer, message),
// and the first one — independent of map iteration order — is used.
func detailOf(ve *jsonschema.ValidationError) string {
	leaves := collectLeaves(ve, nil)
	slices.SortFunc(leaves, func(a, b *jsonschema.ValidationError) int {
		pa, pb := pointerOf(a), pointerOf(b)
		if c := strings.Compare(pa, pb); c != 0 {
			return c
		}
		return strings.Compare(a.ErrorKind.LocalizedString(msgPrinter), b.ErrorKind.LocalizedString(msgPrinter))
	})
	leaf := leaves[0]
	return pointerOf(leaf) + ": " + leaf.ErrorKind.LocalizedString(msgPrinter)
}

// collectLeaves appends every cause-less node reachable from ve to out and
// returns the result. ve itself is a leaf when it has no causes.
func collectLeaves(ve *jsonschema.ValidationError, out []*jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(ve.Causes) == 0 {
		return append(out, ve)
	}
	for _, cause := range ve.Causes {
		out = collectLeaves(cause, out)
	}
	return out
}

func pointerOf(ve *jsonschema.ValidationError) string {
	return "/" + strings.Join(ve.InstanceLocation, "/")
}

// compileFn is swapped out in tests to count and observe compilations
// without changing Cache's public behavior.
var compileFn = Compile

// Cache compiles each distinct key at most once and reuses the result. It is
// safe for concurrent use.
type Cache struct {
	entries sync.Map // key: string -> *cacheEntry
}

type cacheEntry struct {
	once   sync.Once
	schema *Schema
	err    error
}

// NewCache creates an empty Cache.
func NewCache() *Cache { return &Cache{} }

// Get returns the compiled schema for key, compiling raw the first time key
// is seen and reusing that result (including a compile error) afterwards.
func (c *Cache) Get(key string, raw json.RawMessage) (*Schema, error) {
	v, _ := c.entries.LoadOrStore(key, &cacheEntry{})
	e := v.(*cacheEntry)
	e.once.Do(func() {
		e.schema, e.err = compileFn(raw)
	})
	return e.schema, e.err
}
