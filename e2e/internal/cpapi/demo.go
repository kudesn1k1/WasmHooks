package cpapi

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadDemoHook reads checkout.discount from examples/demo/snapshot.json,
// the schemas the M0 demo and the discount module are built around.
func LoadDemoHook(path string) (HookSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return HookSpec{}, fmt.Errorf("read demo snapshot: %w", err)
	}
	var snap struct {
		Hooks []struct {
			Name         string         `json:"name"`
			InputSchema  map[string]any `json:"input_schema"`
			OutputSchema map[string]any `json:"output_schema"`
			SampleInput  map[string]any `json:"sample_input"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return HookSpec{}, fmt.Errorf("%s: %w", path, err)
	}
	for _, h := range snap.Hooks {
		if h.Name == "checkout.discount" {
			return HookSpec{
				InputSchema:          h.InputSchema,
				OutputSchema:         h.OutputSchema,
				TimeoutMS:            50,
				MemoryMaxPages:       64,
				AllowedHostFunctions: []string{},
				AllowedEffectTypes:   []string{},
				SampleInput:          h.SampleInput,
			}, nil
		}
	}
	return HookSpec{}, fmt.Errorf("%s: no hook checkout.discount", path)
}
