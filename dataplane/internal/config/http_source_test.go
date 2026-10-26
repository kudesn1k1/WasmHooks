package config

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// snapshotJSON is a minimal valid snapshot of the given version with one hook.
func snapshotJSON(version int64, hook string) string {
	return fmt.Sprintf(`{"version": %d, "api_keys": [], "tenants": [], "bindings": [],
		"hooks": [{"name": %q, "def_version": 1, "input_schema": {}, "output_schema": {},
		"timeout_ms": 50, "memory_max_pages": 64, "allowed_host_functions": [],
		"allowed_effect_types": [], "sample_input": {}}]}`, version, hook)
}

func TestHTTPSourceRequest(t *testing.T) {
	var gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotQuery = r.Header.Get("Authorization"), r.URL.RawQuery
		if r.URL.Path != "/internal/v1/config/snapshot" {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, snapshotJSON(3, "a"))
	}))
	defer srv.Close()
	src := &HTTPSource{BaseURL: srv.URL + "/", Token: "secret", WaitS: 7}

	snap, err := src.Fetch(context.Background(), -1)
	if err != nil || snap == nil || snap.Version != 3 {
		t.Fatalf("Fetch(-1) = %v, %v", snap, err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotQuery != "" {
		t.Errorf("query without after = %q, want none", gotQuery)
	}

	if _, err := src.Fetch(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "after_version=3&wait_s=7" {
		t.Errorf("query = %q", gotQuery)
	}
}

func TestHTTPSourceStatuses(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantNil bool
		wantErr string
		is      error
	}{
		{name: "304", status: http.StatusNotModified, wantNil: true},
		{name: "401", status: http.StatusUnauthorized, is: ErrUnauthorized},
		{name: "500", status: http.StatusInternalServerError, body: "boom", wantErr: "500"},
		{name: "invalid snapshot", status: http.StatusOK, body: `{"version": 0}`, wantErr: "version"},
		{name: "not json", status: http.StatusOK, body: `<html>`, wantErr: "decode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()
			snap, err := (&HTTPSource{BaseURL: srv.URL}).Fetch(context.Background(), 1)
			switch {
			case tt.wantNil:
				if snap != nil || err != nil {
					t.Fatalf("got %v, %v; want nil, nil", snap, err)
				}
			case tt.is != nil:
				if !errors.Is(err, tt.is) {
					t.Fatalf("err = %v, want %v", err, tt.is)
				}
			default:
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
			}
		})
	}
}

func TestHTTPSourceClientTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	src := &HTTPSource{BaseURL: srv.URL, Client: &http.Client{Timeout: 50 * time.Millisecond}}
	if _, err := src.Fetch(context.Background(), 1); err == nil {
		t.Fatal("want a timeout error")
	}
}

func TestHTTPSourceLoad(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("Load sent a query: %q", r.URL.RawQuery)
		}
		fmt.Fprint(w, snapshotJSON(1, "a"))
	}))
	defer srv.Close()
	var src Source = &HTTPSource{BaseURL: srv.URL}
	snap, err := src.Load(context.Background())
	if err != nil || snap.Version != 1 {
		t.Fatalf("Load = %v, %v", snap, err)
	}
}
