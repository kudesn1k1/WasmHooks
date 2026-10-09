// Package storetest holds the behaviour every modstore.Store must have.
package storetest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
)

// Run checks any modstore.Store against the behaviour every store must have.
// newStore receives the objects the store must contain, keyed by hash. The
// objects keyed by a hash that differs from HashOf(content) must be stored
// as-is, so the store can be checked for tamper detection.
func Run(t *testing.T, newStore func(t *testing.T, objects map[string][]byte) modstore.Store) {
	t.Helper()

	good := []byte("\x00asm real module")
	goodHash := modstore.HashOf(good)
	// Content stored under a hash it does not have.
	tamperedHash := "sha256:" + string(bytes.Repeat([]byte("0"), 64))
	tampered := []byte("not the right bytes")

	store := newStore(t, map[string][]byte{
		goodHash:     good,
		tamperedHash: tampered,
	})
	ctx := context.Background()

	t.Run("present module returns its bytes", func(t *testing.T) {
		got, err := store.Get(ctx, goodHash)
		if err != nil {
			t.Fatalf("Get(%q) error: %v", goodHash, err)
		}
		if !bytes.Equal(got, good) {
			t.Fatalf("Get(%q) = %q, want %q", goodHash, got, good)
		}
	})

	t.Run("unknown hash is ErrNotFound", func(t *testing.T) {
		_, err := store.Get(ctx, "sha256:"+string(bytes.Repeat([]byte("1"), 64)))
		if !errors.Is(err, modstore.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("content under another hash is ErrHashMismatch", func(t *testing.T) {
		_, err := store.Get(ctx, tamperedHash)
		if !errors.Is(err, modstore.ErrHashMismatch) {
			t.Fatalf("error = %v, want ErrHashMismatch", err)
		}
	})

	t.Run("malformed hash is ErrBadHash", func(t *testing.T) {
		valid := goodHash[len("sha256:"):]
		for name, hash := range map[string]string{
			"no prefix":      valid,
			"upper case":     "sha256:" + string(bytes.ToUpper([]byte(valid))),
			"path traversal": "sha256:../..",
			"empty":          "",
		} {
			if _, err := store.Get(ctx, hash); !errors.Is(err, modstore.ErrBadHash) {
				t.Errorf("%s: Get(%q) error = %v, want ErrBadHash", name, hash, err)
			}
		}
	})

	t.Run("canceled context is an error", func(t *testing.T) {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := store.Get(canceled, goodHash); err == nil {
			t.Fatal("Get with canceled context succeeded, want error")
		}
	})
}
