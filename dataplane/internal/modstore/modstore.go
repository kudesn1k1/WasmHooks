// Package modstore fetches wasm module bytes by their content hash. A
// module's address is derived from its content, never assigned: two byte
// slices with equal SHA-256 digests are the same module.
package modstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var (
	// ErrNotFound: no module is stored at the requested hash.
	ErrNotFound = errors.New("modstore: module not found")
	// ErrHashMismatch: the stored bytes don't hash to the requested value.
	ErrHashMismatch = errors.New("modstore: content hash mismatch")
	// ErrBadHash: the hash string isn't "sha256:" followed by 64 lowercase
	// hex characters.
	ErrBadHash = errors.New("modstore: malformed module hash")
)

// hashFormat matches a well-formed module hash: "sha256:" plus 64 lowercase
// hex characters, nothing else. Any path-traversal payload (e.g.
// "sha256:../..") fails this pattern before it ever reaches a filesystem
// path.
var hashFormat = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Store fetches module bytes by content hash.
type Store interface {
	Get(ctx context.Context, hash string) ([]byte, error)
}

// HashOf returns the content hash of wasm: "sha256:" plus the lowercase hex
// SHA-256 digest.
func HashOf(wasm []byte) string {
	sum := sha256.Sum256(wasm)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ParseHash validates hash and returns its hex digest. hash must be exactly
// "sha256:" followed by 64 lowercase hex characters.
func ParseHash(hash string) (hexDigest string, err error) {
	if !hashFormat.MatchString(hash) {
		return "", fmt.Errorf("%w: %q", ErrBadHash, hash)
	}
	return hash[len("sha256:"):], nil
}

// FS is a Store backed by a directory of "<hex>.wasm" files.
type FS struct {
	dir string
}

// NewFS creates an FS rooted at dir. dir is not created or validated until
// the first Get.
func NewFS(dir string) *FS { return &FS{dir: dir} }

// Get reads the module for hash and verifies its content matches. ctx is
// accepted for interface compatibility with a future networked Store; this
// implementation does local file I/O only and does not honor cancellation.
func (s *FS) Get(ctx context.Context, hash string) ([]byte, error) {
	hexDigest, err := ParseHash(hash)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.dir, hexDigest+".wasm")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
		}
		return nil, fmt.Errorf("modstore: read %s: %w", path, err)
	}
	if HashOf(data) != hash {
		return nil, fmt.Errorf("%w: %s", ErrHashMismatch, hash)
	}
	return data, nil
}
