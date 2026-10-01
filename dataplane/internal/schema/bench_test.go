package schema

import (
	"encoding/json"
	"testing"
)

// discountInputSchema mirrors the checkout.discount hook's input_schema:
// the payload shape used by the discount fixture in
// internal/sandbox/sandboxtest (Task 5), also shown as sample_input in the
// M0 design spec section 6.2.
const discountInputSchema = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"type": "object",
	"required": ["cart_total", "customer"],
	"properties": {
		"cart_total": {"type": "number"},
		"customer": {
			"type": "object",
			"required": ["id", "lifetime_spend"],
			"properties": {
				"id": {"type": "string"},
				"lifetime_spend": {"type": "number"}
			}
		}
	}
}`

// discountPayload is the exact input the discount fixture is called with in
// sandboxtest.Run's CallOK case.
const discountPayload = `{"cart_total":10,"customer":{"id":"c1","lifetime_spend":600}}`

func BenchmarkValidate(b *testing.B) {
	s, err := Compile(json.RawMessage(discountInputSchema))
	if err != nil {
		b.Fatalf("Compile(): %v", err)
	}
	payload := []byte(discountPayload)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := s.Validate(payload); err != nil {
			b.Fatalf("Validate(): %v", err)
		}
	}
}
