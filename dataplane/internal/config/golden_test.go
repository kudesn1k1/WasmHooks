package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestControlPlaneGoldenSnapshot parses the snapshot the control plane
// produces for a fixed data set (api/testdata/snapshot.golden.json, written by
// control-plane/tests/test_golden.py). If this fails, the control plane emits
// snapshots the data plane rejects: the data plane would stop applying config.
func TestControlPlaneGoldenSnapshot(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "testdata", "snapshot.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	v := NewView(snap)
	b, ok := v.Binding("merchant-a", "checkout.discount")
	if !ok {
		t.Fatal("binding merchant-a/checkout.discount missing")
	}
	if b.Config["threshold"] != "1000" {
		t.Fatalf("binding config = %v, want threshold 1000", b.Config)
	}
	z, ok := v.Binding("merchant-z", "checkout.discount")
	if !ok || z.Config == nil || len(z.Config) != 0 {
		t.Fatalf("binding merchant-z with an empty config = %+v, %v", z, ok)
	}
	h, ok := v.Hook("order.validate")
	if !ok || len(h.AllowedEffectTypes) != 2 || len(h.AllowedHostFunctions) != 1 {
		t.Fatalf("hook order.validate allowed lists = %+v", h)
	}
	if !v.AuthenticateToken("whk_golden") {
		t.Fatal("golden API key does not authenticate")
	}
}
