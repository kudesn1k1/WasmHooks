package load

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
)

// WriteTable prints one row per tenant and phase: calls, latency from send to
// response, and the count of every outcome.
func WriteTable(w io.Writer, res Result) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TENANT\tPHASE\tN\tP50\tP99\tMAX\tOUTCOMES")
	for _, p := range []Phase{res.AQuiet, res.ANoisy, res.BNoisy} {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
			p.Tenant, p.Name, p.N, fmtMS(p.P50), fmtMS(p.P99), fmtMS(p.Max), outcomes(p.Outcomes))
	}
	tw.Flush()
}

func outcomes(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", k, m[k])
	}
	return strings.Join(parts, " ")
}

func fmtMS(d time.Duration) string {
	return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
}

// Violations returns what breaks the thresholds of spec M2 §9.3: merchant-a
// is 100% ok in both phases, its p99 in the noisy phase is at most its quiet
// p99 plus cfg.MaxP99Growth, and merchant-b has more than cfg.MinBShare
// quota_exceeded and no timeout.
func Violations(cfg Config, res Result) []string {
	var out []string
	for _, p := range []Phase{res.AQuiet, res.ANoisy} {
		if p.N == 0 {
			out = append(out, fmt.Sprintf("merchant-a %s: no calls were measured", p.Name))
		} else if ok := p.Outcomes["ok"]; ok != p.N {
			out = append(out, fmt.Sprintf("merchant-a %s: %d of %d calls ok (%s)", p.Name, ok, p.N, outcomes(p.Outcomes)))
		}
	}
	if grew := res.ANoisy.P99 - res.AQuiet.P99; grew > cfg.MaxP99Growth {
		out = append(out, fmt.Sprintf("merchant-a p99 grew by %s (%s -> %s), limit %s",
			fmtMS(grew), fmtMS(res.AQuiet.P99), fmtMS(res.ANoisy.P99), fmtMS(cfg.MaxP99Growth)))
	}
	b := res.BNoisy
	if b.N == 0 {
		out = append(out, "merchant-b: no calls were measured")
	} else if share := float64(b.Outcomes["quota_exceeded"]) / float64(b.N); share < cfg.MinBShare {
		out = append(out, fmt.Sprintf("merchant-b: quota_exceeded share %.1f%%, want at least %.1f%% (%s)",
			share*100, cfg.MinBShare*100, outcomes(b.Outcomes)))
	}
	if n := b.Outcomes["timeout"]; n > 0 {
		out = append(out, fmt.Sprintf("merchant-b: %d calls timed out", n))
	}
	return out
}
