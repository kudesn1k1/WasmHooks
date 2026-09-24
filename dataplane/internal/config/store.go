package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
)

// Source loads a configuration snapshot from wherever it lives: a file in
// M0, an HTTP long-poll endpoint once the control plane exists.
type Source interface {
	Load(ctx context.Context) (*Snapshot, error)
}

// FileSource loads a snapshot from a local file, in exactly the JSON format
// documented in the M0 design spec section 6.2.
type FileSource struct {
	Path string
}

// Load reads and parses the file at Path.
func (s FileSource) Load(ctx context.Context) (*Snapshot, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", s.Path, err)
	}
	return Parse(data)
}

// ErrStaleSnapshot is returned by Store.Update when the given snapshot's
// version is not strictly newer than the current one.
var ErrStaleSnapshot = errors.New("config: snapshot version is not newer")

// Store holds the current configuration View and swaps it atomically as new
// snapshots arrive. It is safe for concurrent use.
type Store struct {
	cur atomic.Pointer[View]
}

// NewStore creates an empty Store. Current returns nil until the first
// successful Update.
func NewStore() *Store { return &Store{} }

// Current returns the current View, or nil if Update has never succeeded.
func (s *Store) Current() *View { return s.cur.Load() }

// Update installs snap as the current snapshot if its version is strictly
// newer than the current one. Concurrent Update calls are linearized by a
// compare-and-swap loop, so the highest version submitted always wins
// regardless of goroutine scheduling.
func (s *Store) Update(snap *Snapshot) error {
	for {
		old := s.cur.Load()
		if old != nil && snap.Version <= old.Version() {
			return ErrStaleSnapshot
		}
		next := NewView(snap)
		if s.cur.CompareAndSwap(old, next) {
			return nil
		}
	}
}
