package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrUnauthorized: the control plane rejected the internal token. Retrying
// does not help until someone fixes the configuration, so it is logged as
// such.
var ErrUnauthorized = errors.New("config: control plane rejected the internal token")

// maxSnapshotBytes caps a snapshot body. A real installation is far below it;
// the cap only stops a misbehaving server from exhausting memory.
const maxSnapshotBytes = 64 << 20

// HTTPSource fetches snapshots from the control plane's long-poll endpoint
// (GET /internal/v1/config/snapshot, api/dataplane-internal.openapi.yaml).
type HTTPSource struct {
	BaseURL string       // e.g. http://control-plane:8000
	Token   string       // internal bearer token
	WaitS   int          // long-poll wait, 1..60; 0 means 30
	Client  *http.Client // nil means a client with Timeout = WaitS + 10s and a 5s dial timeout

	once          sync.Once
	defaultClient *http.Client
}

func (s *HTTPSource) waitS() int {
	if s.WaitS <= 0 {
		return 30
	}
	return s.WaitS
}

func (s *HTTPSource) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	s.once.Do(func() {
		longPoll := time.Duration(s.waitS()+10) * time.Second
		s.defaultClient = &http.Client{
			Timeout: longPoll,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				// A control plane that moved to a new address (a restarted
				// container, a rescheduled pod) must not cost a 30 s dial to
				// the old one: fail fast and let the backoff retry.
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: longPoll,
				MaxIdleConns:          2,
				IdleConnTimeout:       90 * time.Second,
			},
		}
	})
	return s.defaultClient
}

// Fetch asks for a snapshot newer than after; after < 0 asks for the current
// one without waiting. It returns (nil, nil) on 304: nothing changed within
// the wait.
func (s *HTTPSource) Fetch(ctx context.Context, after int64) (*Snapshot, error) {
	u := strings.TrimRight(s.BaseURL, "/") + "/internal/v1/config/snapshot"
	if after >= 0 {
		q := url.Values{}
		q.Set("after_version", strconv.FormatInt(after, 10))
		q.Set("wait_s", strconv.Itoa(s.waitS()))
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("config: build snapshot request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)

	resp, err := s.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("config: fetch snapshot: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxSnapshotBytes+1))
		if err != nil {
			return nil, fmt.Errorf("config: read snapshot: %w", err)
		}
		if len(body) > maxSnapshotBytes {
			return nil, fmt.Errorf("config: snapshot exceeds %d bytes", maxSnapshotBytes)
		}
		return Parse(body)
	case http.StatusNotModified:
		return nil, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrUnauthorized
	default:
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("config: control plane answered %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
}

// Load implements Source: the current snapshot, no waiting.
func (s *HTTPSource) Load(ctx context.Context) (*Snapshot, error) {
	snap, err := s.Fetch(ctx, -1)
	if err == nil && snap == nil {
		return nil, errors.New("config: control plane answered 304 to a request without after_version")
	}
	return snap, err
}
