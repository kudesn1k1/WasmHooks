package modstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
