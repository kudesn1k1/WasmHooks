package load

import (
	"strings"
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	sorted := []time.Duration{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, tc := range []struct {
		q    float64
		want time.Duration
	}{{0.5, 5}, {0.99, 10}, {0.1, 1}, {0, 1}, {1, 10}} {
		if got := percentile(sorted, tc.q); got != tc.want {
			t.Errorf("percentile(%v) = %v, want %v", tc.q, got, tc.want)
		}
	}
}

func TestViolations(t *testing.T) {
	cfg := Config{MaxP99Growth: 10 * time.Millisecond, MinBShare: 0.9}
	planned := 12 * time.Second
	good := func() Result {
		return Result{
			AQuiet: Phase{Tenant: "a", Name: "quiet", N: 500, P99: 2 * time.Millisecond,
				Outcomes: map[string]int{"ok": 500}, Planned: planned, Sending: planned},
			ANoisy: Phase{Tenant: "a", Name: "noisy", N: 500, P99: 3 * time.Millisecond,
				Outcomes: map[string]int{"ok": 500}, Planned: planned, Sending: planned},
			BNoisy: Phase{Tenant: "b", Name: "noisy", N: 50000,
				Outcomes: map[string]int{"ok": 500, "quota_exceeded": 49500}, Planned: planned, Sending: planned},
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*Result)
		want   string // "" means no violation
	}{
		{"pass", func(*Result) {}, ""},
		{"merchant-a not ok", func(r *Result) { r.ANoisy.Outcomes = map[string]int{"ok": 499, "unavailable": 1} }, "499 of 500 calls ok"},
		{"merchant-a measured nothing", func(r *Result) { r.AQuiet.N = 0 }, "no calls were measured"},
		{"p99 grew too much", func(r *Result) { r.ANoisy.P99 = 13 * time.Millisecond }, "p99 grew"},
		{"quota share exactly at the bound", func(r *Result) {
			r.BNoisy.Outcomes = map[string]int{"ok": 5000, "quota_exceeded": 45000}
		}, "quota_exceeded share"},
		{"merchant-b timed out", func(r *Result) { r.BNoisy.Outcomes["timeout"] = 1 }, "timed out"},
		{"generator fell behind", func(r *Result) { r.BNoisy.Sending = 14 * time.Second }, "fell behind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := good()
			tc.change(&r)
			got := strings.Join(Violations(cfg, r), "\n")
			switch {
			case tc.want == "" && got != "":
				t.Fatalf("unexpected violations:\n%s", got)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Fatalf("violations %q, want one containing %q", got, tc.want)
			}
		})
	}
}
