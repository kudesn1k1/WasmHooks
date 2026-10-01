package modstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hex64 returns a 64-character lowercase hex string, built by repetition so
// its length can't be miscounted by hand.
func hex64(fill rune) string { return strings.Repeat(string(fill), 64) }

func TestHashOf(t *testing.T) {
	wasm := []byte("\x00asm fake module bytes")
	got := HashOf(wasm)

	sum := sha256.Sum256(wasm)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("HashOf() = %q, want %q", got, want)
	}
	if got != HashOf(wasm) {
		t.Fatalf("HashOf is not deterministic")
	}
	if !hashFormat.MatchString(got) {
		t.Fatalf("HashOf() = %q does not match sha256:[0-9a-f]{64}", got)
	}
}

func TestParseHash(t *testing.T) {
	validHex := hex64('a')
	tests := []struct {
		name    string
		hash    string
		wantHex string
		wantErr error
	}{
		{name: "valid", hash: "sha256:" + validHex, wantHex: validHex},
		{name: "no prefix", hash: validHex, wantErr: ErrBadHash},
		{name: "wrong prefix", hash: "md5:" + validHex, wantErr: ErrBadHash},
		{name: "too short", hash: "sha256:" + validHex[:63], wantErr: ErrBadHash},
		{name: "too long", hash: "sha256:" + validHex + "a", wantErr: ErrBadHash},
		{name: "uppercase", hash: "sha256:" + validHex[:63] + "A", wantErr: ErrBadHash},
		{name: "non-hex", hash: "sha256:" + validHex[:63] + "g", wantErr: ErrBadHash},
		{name: "path traversal", hash: "sha256:../..", wantErr: ErrBadHash},
		{name: "empty", hash: "", wantErr: ErrBadHash},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHash(tt.hash)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParseHash(%q) error = %v, want %v", tt.hash, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseHash(%q) unexpected error: %v", tt.hash, err)
			}
			if got != tt.wantHex {
				t.Fatalf("ParseHash(%q) = %q, want %q", tt.hash, got, tt.wantHex)
			}
		})
	}
}

func writeModule(t *testing.T, dir string, content []byte) string {
	t.Helper()
	hash := HashOf(content)
	hexDigest, err := ParseHash(hash)
	if err != nil {
		t.Fatalf("ParseHash: %v", err)
	}
	path := filepath.Join(dir, hexDigest+".wasm")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return hash
}

func TestFS_Get(t *testing.T) {
	dir := t.TempDir()
	content := []byte("\x00asm real module")
	hash := writeModule(t, dir, content)

	// Write a file whose name doesn't match its content, to test hash
	// mismatch detection.
	tamperedHex := hex64('0')
	if err := os.WriteFile(filepath.Join(dir, tamperedHex+".wasm"), []byte("not the right bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tamperedHash := "sha256:" + tamperedHex

	fs := NewFS(dir)
	ctx := context.Background()

	t.Run("matching hash returns bytes", func(t *testing.T) {
		got, err := fs.Get(ctx, hash)
		if err != nil {
			t.Fatalf("Get(%q) error: %v", hash, err)
		}
		if string(got) != string(content) {
			t.Fatalf("Get(%q) = %q, want %q", hash, got, content)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		missingHex := hex64('1')
		_, err := fs.Get(ctx, "sha256:"+missingHex)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("tampered content", func(t *testing.T) {
		_, err := fs.Get(ctx, tamperedHash)
		if !errors.Is(err, ErrHashMismatch) {
			t.Fatalf("Get(tampered) error = %v, want ErrHashMismatch", err)
		}
	})

	t.Run("bad hash format", func(t *testing.T) {
		_, err := fs.Get(ctx, "sha256:not-hex")
		if !errors.Is(err, ErrBadHash) {
			t.Fatalf("Get(bad hash) error = %v, want ErrBadHash", err)
		}
	})

	t.Run("path traversal rejected as bad hash", func(t *testing.T) {
		_, err := fs.Get(ctx, "sha256:../..")
		if !errors.Is(err, ErrBadHash) {
			t.Fatalf("Get(traversal) error = %v, want ErrBadHash", err)
		}
	})
}

// Store must be satisfied by *FS.
var _ Store = (*FS)(nil)
