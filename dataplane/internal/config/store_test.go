package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStore_CurrentNilBeforeUpdate(t *testing.T) {
	s := NewStore()
	if v := s.Current(); v != nil {
		t.Fatalf("Current() before any Update = %+v, want nil", v)
	}
}

func TestStore_Update(t *testing.T) {
	s := NewStore()

	snap3 := mustParse(t, validSnapshotJSON) // version 42; give it version 3 for this test
	snap3.Version = 3
	if err := s.Update(snap3); err != nil {
		t.Fatalf("Update(v3): %v", err)
	}
	if got := s.Current().Version(); got != 3 {
		t.Fatalf("Current().Version() = %d, want 3", got)
	}

	snap2 := mustParse(t, validSnapshotJSON)
	snap2.Version = 2
	if err := s.Update(snap2); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("Update(v2) after v3 error = %v, want ErrStaleSnapshot", err)
	}
	if got := s.Current().Version(); got != 3 {
		t.Fatalf("Current().Version() after stale Update = %d, want unchanged 3", got)
	}

	// Equal version is also stale (must be strictly newer).
	snap3b := mustParse(t, validSnapshotJSON)
	snap3b.Version = 3
	if err := s.Update(snap3b); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("Update(v3) after v3 error = %v, want ErrStaleSnapshot", err)
	}

	snap4 := mustParse(t, validSnapshotJSON)
	snap4.Version = 4
	if err := s.Update(snap4); err != nil {
		t.Fatalf("Update(v4): %v", err)
	}
	if got := s.Current().Version(); got != 4 {
		t.Fatalf("Current().Version() = %d, want 4", got)
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore()
	base := mustParse(t, validSnapshotJSON)

	var wg sync.WaitGroup
	for i := 1; i <= 100; i++ {
		version := int64(i)
		wg.Go(func() {
			snap := *base
			snap.Version = version
			s.Update(&snap)
		})
	}
	for range 200 {
		wg.Go(func() {
			_ = s.Current()
		})
	}
	wg.Wait()

	final := s.Current()
	if final == nil {
		t.Fatal("Current() is nil after concurrent updates")
	}
	if final.Version() != 100 {
		t.Fatalf("Current().Version() = %d, want 100 (highest version wins)", final.Version())
	}
}

func TestFileSource_Load(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	if err := os.WriteFile(path, []byte(validSnapshotJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	src := FileSource{Path: path}
	snap, err := src.Load(context.Background())
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if snap.Version != 42 {
		t.Fatalf("Load().Version = %d, want 42", snap.Version)
	}
}

func TestFileSource_Load_MissingFile(t *testing.T) {
	src := FileSource{Path: filepath.Join(t.TempDir(), "does-not-exist.json")}
	if _, err := src.Load(context.Background()); err == nil {
		t.Fatal("Load() on a missing file should fail")
	}
}

func TestFileSource_Load_InvalidSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	if err := os.WriteFile(path, []byte(`{"version": 0}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	src := FileSource{Path: path}
	if _, err := src.Load(context.Background()); err == nil {
		t.Fatal("Load() on an invalid snapshot should fail")
	}
}

// Source must be satisfied by FileSource.
var _ Source = FileSource{}
