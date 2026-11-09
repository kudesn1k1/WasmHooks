package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// controlPlane is a stand-in for the real control plane: it serves config
// snapshots on the same long-poll endpoint the data plane polls in
// production (GET /internal/v1/config/snapshot, api/dataplane-internal.openapi.yaml),
// so a quota changed here reaches the running data plane the same way a
// PATCH through the real API does. The real path is proven by e2e steps 8-9.
type controlPlane struct {
	token string
	srv   *http.Server
	url   string

	mu      sync.Mutex
	version int64
	body    []byte
	changed chan struct{} // closed and replaced on every change
	sentAt  chan time.Time
}

func startControlPlane(token string) (*controlPlane, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	cp := &controlPlane{token: token, changed: make(chan struct{}), sentAt: make(chan time.Time, 16)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/v1/config/snapshot", cp.snapshot)
	cp.srv = &http.Server{Handler: mux}
	cp.url = "http://" + ln.Addr().String()
	go cp.srv.Serve(ln)
	return cp, nil
}

func (cp *controlPlane) close() { cp.srv.Close() }

// set publishes a new snapshot under the next version.
func (cp *controlPlane) set(snap scene) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.version++
	cp.body, _ = json.Marshal(snap.snapshot(cp.version))
	close(cp.changed)
	cp.changed = make(chan struct{})
}

// snapshot answers a long-poll: the current snapshot if it is newer than
// after_version, otherwise wait for a change or wait_s, then 304.
func (cp *controlPlane) snapshot(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+cp.token {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	after := int64(-1)
	if v := r.URL.Query().Get("after_version"); v != "" {
		after, _ = strconv.ParseInt(v, 10, 64)
	}
	waitS, _ := strconv.Atoi(r.URL.Query().Get("wait_s"))
	timer := time.NewTimer(time.Duration(max(waitS, 1)) * time.Second)
	defer timer.Stop()
	for {
		cp.mu.Lock()
		version, body, changed := cp.version, cp.body, cp.changed
		cp.mu.Unlock()
		if version > after {
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
			select {
			case cp.sentAt <- time.Now():
			default:
			}
			return
		}
		select {
		case <-changed:
		case <-timer.C:
			w.WriteHeader(http.StatusNotModified)
			return
		case <-r.Context().Done():
			return
		}
	}
}

// waitDelivered returns when the data plane has fetched a snapshot sent
// at or after since, or after limit.
func (cp *controlPlane) waitDelivered(since time.Time, limit time.Duration) (time.Time, bool) {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for {
		select {
		case at := <-cp.sentAt:
			// Not After: on Windows both readings can fall in one clock tick.
			if !at.Before(since) {
				return at, true
			}
		case <-timer.C:
			return time.Time{}, false
		}
	}
}

// scene is the configuration of one act: what script shop B runs and its
// quotas. Shop A always runs the discount script with default quotas.
type scene struct {
	bModule      string // module hash bound to shop B
	bRateRPS     int    // 0: unlimited
	bConcurrency int
}

func (s scene) snapshot(version int64) map[string]any {
	sum := sha256.Sum256([]byte(apiKey))
	discountConfig := map[string]string{"threshold": "1000", "percent": "10"}
	return map[string]any{
		"version":  version,
		"api_keys": []any{map[string]any{"id": "k1", "name": "demo", "sha256": hex.EncodeToString(sum[:])}},
		"hooks": []any{map[string]any{
			"name": hookName, "def_version": 1,
			"input_schema": map[string]any{
				"type": "object", "required": []string{"cart_total", "customer"},
				"properties": map[string]any{
					"cart_total": map[string]any{"type": "number"},
					"customer": map[string]any{
						"type": "object", "required": []string{"id", "lifetime_spend"},
						"properties": map[string]any{"id": map[string]any{"type": "string"}, "lifetime_spend": map[string]any{"type": "number"}},
					},
				},
			},
			"output_schema":          map[string]any{"type": "object", "required": []string{"discount_percent", "reason"}},
			"timeout_ms":             hookTimeout.Milliseconds(),
			"memory_max_pages":       64,
			"allowed_host_functions": []string{},
			"allowed_effect_types":   []string{},
			"sample_input":           map[string]any{"cart_total": 1, "customer": map[string]any{"id": "c", "lifetime_spend": 0}},
		}},
		"tenants": []any{
			map[string]any{"external_id": shopA, "concurrency_limit": 64, "rate_limit_rps": 0},
			map[string]any{"external_id": shopB, "concurrency_limit": s.bConcurrency, "rate_limit_rps": s.bRateRPS},
		},
		"bindings": []any{
			map[string]any{"tenant_id": shopA, "hook": hookName, "module_hash": modules.discount, "config": discountConfig, "config_version": 1},
			map[string]any{"tenant_id": shopB, "hook": hookName, "module_hash": s.bModule, "config": discountConfig, "config_version": version},
		},
	}
}
