package executor

import (
	"errors"
	"strings"
	"testing"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
)

func TestParseOutput(t *testing.T) {
	const max = 1 << 10
	cases := []struct {
		name        string
		raw         string
		allowed     []string
		idemKey     string
		wantResult  string
		wantKeys    []string
		wantReason  string
		wantInError string
	}{
		{name: "result only", raw: `{"result":{"a":1}}`, wantResult: `{"a":1}`},
		{name: "explicit empty effects", raw: `{"result":{},"effects":[]}`, wantResult: `{}`},
		{name: "effect keys from idempotency key", raw: `{"result":{},"effects":[{"type":"x","payload":{}},{"type":"x","payload":{"n":1}}]}`,
			allowed: []string{"x"}, idemKey: "k", wantResult: `{}`, wantKeys: []string{"k:0", "k:1"}},
		{name: "explicit key kept", raw: `{"result":{},"effects":[{"type":"x","key":"custom","payload":{}}]}`,
			allowed: []string{"x"}, idemKey: "k", wantResult: `{}`, wantKeys: []string{"custom"}},
		{name: "no idempotency key", raw: `{"result":{},"effects":[{"type":"x","payload":{}}]}`,
			allowed: []string{"x"}, wantResult: `{}`, wantKeys: []string{""}},
		{name: "effect payload optional", raw: `{"result":{},"effects":[{"type":"x"}]}`,
			allowed: []string{"x"}, wantResult: `{}`, wantKeys: []string{""}},
		{name: "effect type not allowed", raw: `{"result":{},"effects":[{"type":"y","payload":{}}]}`,
			allowed: []string{"x"}, wantReason: execproto.ReasonInvalidEffect, wantInError: `"y"`},
		{name: "effect without type", raw: `{"result":{},"effects":[{"payload":{}}]}`,
			allowed: []string{"x"}, wantReason: execproto.ReasonInvalidEffect},
		{name: "effect payload not object", raw: `{"result":{},"effects":[{"type":"x","payload":5}]}`,
			allowed: []string{"x"}, wantReason: execproto.ReasonInvalidEffect},
		{name: "not json", raw: `hello`, wantReason: execproto.ReasonInvalidOutput},
		{name: "array", raw: `[1]`, wantReason: execproto.ReasonInvalidOutput},
		{name: "missing result", raw: `{"effects":[]}`, wantReason: execproto.ReasonInvalidOutput, wantInError: "result"},
		{name: "result not object", raw: `{"result":[1]}`, wantReason: execproto.ReasonInvalidOutput},
		{name: "result null", raw: `{"result":null}`, wantReason: execproto.ReasonInvalidOutput},
		{name: "effects not array", raw: `{"result":{},"effects":{}}`, wantReason: execproto.ReasonInvalidOutput},
		{name: "too large", raw: `{"result":{"s":"` + strings.Repeat("a", max) + `"}}`, wantReason: execproto.ReasonInvalidOutput, wantInError: "exceeds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, effects, err := parseOutput([]byte(tc.raw), max, tc.allowed, tc.idemKey)
			if tc.wantReason != "" {
				var oe *outputError
				if !errors.As(err, &oe) {
					t.Fatalf("want outputError, got %v", err)
				}
				if oe.reason != tc.wantReason {
					t.Fatalf("reason = %q, want %q (%v)", oe.reason, tc.wantReason, err)
				}
				if tc.wantInError != "" && !strings.Contains(err.Error(), tc.wantInError) {
					t.Fatalf("error %q does not mention %q", err, tc.wantInError)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(result) != tc.wantResult {
				t.Fatalf("result = %s, want %s", result, tc.wantResult)
			}
			if len(effects) != len(tc.wantKeys) {
				t.Fatalf("effects = %d, want %d", len(effects), len(tc.wantKeys))
			}
			for i, e := range effects {
				if e.Key != tc.wantKeys[i] {
					t.Errorf("effect %d key = %q, want %q", i, e.Key, tc.wantKeys[i])
				}
				if e.Type != "x" {
					t.Errorf("effect %d type = %q", i, e.Type)
				}
			}
		})
	}
}

func TestParseOutputEffectPayloadPreserved(t *testing.T) {
	_, effects, err := parseOutput([]byte(`{"result":{},"effects":[{"type":"x","payload":{"n":1}}]}`), 1<<10, []string{"x"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(effects[0].Payload) != `{"n":1}` {
		t.Fatalf("payload = %s", effects[0].Payload)
	}
}

func TestTruncateUTF8(t *testing.T) {
	s := strings.Repeat("я", 10) // 2 bytes per rune
	// 5 bytes would split the third rune: keep 2 runes (4 bytes).
	if got := truncate(s, 5); got != "яя" {
		t.Fatalf("truncate = %q, want %q", got, "яя")
	}
	if truncate("short", 100) != "short" {
		t.Fatal("short string must be unchanged")
	}
}
