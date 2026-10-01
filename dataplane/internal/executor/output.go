package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
)

// outputError is a script output that breaks the hook contract. It carries
// the execproto reason; the outcome is always handler_error.
type outputError struct {
	reason string
	detail string
}

func (e *outputError) Error() string { return e.reason + ": " + e.detail }

func invalidOutput(format string, args ...any) error {
	return &outputError{reason: execproto.ReasonInvalidOutput, detail: fmt.Sprintf(format, args...)}
}

func invalidEffect(format string, args ...any) error {
	return &outputError{reason: execproto.ReasonInvalidEffect, detail: fmt.Sprintf(format, args...)}
}

type rawOutput struct {
	Result  json.RawMessage `json:"result"`
	Effects json.RawMessage `json:"effects"`
}

type rawEffect struct {
	Type    string          `json:"type"`
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload"`
}

// parseOutput splits a script's output envelope {"result": {...},
// "effects": [...]} into the result object and validated effects. Effects
// without a key get "<idemKey>:<index>" so two effects of one call never
// collapse under deduplication.
func parseOutput(raw []byte, maxBytes int, allowedEffects []string, idemKey string) ([]byte, []execproto.Effect, error) {
	if len(raw) > maxBytes {
		return nil, nil, invalidOutput("output of %d bytes exceeds the %d byte limit", len(raw), maxBytes)
	}
	var out rawOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, invalidOutput("output is not a JSON object: %v", err)
	}
	if !isJSONObject(out.Result) {
		return nil, nil, invalidOutput(`"result" must be a JSON object`)
	}

	var effects []execproto.Effect
	if len(out.Effects) > 0 && !bytes.Equal(out.Effects, []byte("null")) {
		var rawEffects []rawEffect
		if err := json.Unmarshal(out.Effects, &rawEffects); err != nil {
			return nil, nil, invalidOutput(`"effects" must be an array of objects: %v`, err)
		}
		for i, re := range rawEffects {
			if re.Type == "" {
				return nil, nil, invalidEffect("effect %d has no type", i)
			}
			if !slices.Contains(allowedEffects, re.Type) {
				return nil, nil, invalidEffect("effect type %q is not allowed for this hook", re.Type)
			}
			payload := []byte(re.Payload)
			if len(payload) == 0 {
				payload = []byte("{}")
			} else if !isJSONObject(payload) {
				return nil, nil, invalidEffect("effect %d payload must be a JSON object", i)
			}
			key := re.Key
			if key == "" && idemKey != "" {
				key = idemKey + ":" + strconv.Itoa(i)
			}
			effects = append(effects, execproto.Effect{Type: re.Type, Key: key, Payload: payload})
		}
	}
	return []byte(out.Result), effects, nil
}

func isJSONObject(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{'
}

// truncate cuts s to at most max bytes without splitting a UTF-8 rune.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
