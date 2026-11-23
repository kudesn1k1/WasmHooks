package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/cluster"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/gateway"
)

func diagnostics(f flags, peers *cluster.Cluster, gw *gateway.Gateway) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/v1/cluster", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+f.internalToken)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		stats := peers.Stats()
		total := stats.RetiredAttempts
		for _, count := range stats.Attempts {
			total += count
		}
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"members":               peers.Members(),
			"owners":                peers.Owners(q.Get("tenant"), q.Get("hook")),
			"attempts":              total,
			"attempts_by_executor":  stats.Attempts,
			"retries":               stats.Retries,
			"retry_successes":       stats.SuccessfulRetries,
			"gateway_metrics":       gw.Metrics(),
			"discovery_interval_ms": f.discoveryInterval.Milliseconds(),
			"health_timeout_ms":     f.healthTimeout.Milliseconds(),
			"retry_min_budget_ms":   f.retryBudget.Milliseconds(),
			"breaker_threshold":     f.breakerThreshold,
			"breaker_cooldown_ms":   f.breakerCooldown.Milliseconds(),
		})
	})
	return mux
}
