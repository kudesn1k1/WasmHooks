// Command buildscripts rebuilds the WebAssembly test fixtures from the Rust
// crates in examples/scripts/rust and copies them into testdata/wasm.
//
// Usage (from dataplane/):
//
//	go run ./tools/buildscripts
//
// Crate names are read from the "members" list of the workspace Cargo.toml.
// Cargo writes <name_with_underscores>.wasm; the fixture is saved as
// testdata/wasm/<name-with-dashes>.wasm. For every fixture the tool prints
// its name, size and sha256. Any failure exits with a non-zero code.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	workspaceDir = "../examples/scripts/rust" // relative to dataplane/
	outDir       = "testdata/wasm"
	wasmTarget   = "wasm32-unknown-unknown"
)

func main() {
	if err := run(os.Stdout, os.Stderr); err != nil {
		slog.Error("buildscripts failed", "err", err)
		os.Exit(1)
	}
}

func run(stdout, stderr io.Writer) error {
	manifest := filepath.Join(workspaceDir, "Cargo.toml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		return fmt.Errorf("read workspace manifest (run from dataplane/): %w", err)
	}
	members, err := parseMembers(string(data))
	if err != nil {
		return fmt.Errorf("%s: %w", manifest, err)
	}

	cmd := exec.Command("cargo", "build", "--release", "--target", wasmTarget)
	cmd.Dir = workspaceDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cargo build: %w", err)
	}

	targetDir := os.Getenv("CARGO_TARGET_DIR")
	switch {
	case targetDir == "":
		targetDir = filepath.Join(workspaceDir, "target")
	case !filepath.IsAbs(targetDir):
		// cargo resolves a relative CARGO_TARGET_DIR against its working directory.
		targetDir = filepath.Join(workspaceDir, targetDir)
	}
	releaseDir := filepath.Join(targetDir, wasmTarget, "release")

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}
	for _, name := range members {
		src := filepath.Join(releaseDir, strings.ReplaceAll(name, "-", "_")+".wasm")
		dst := filepath.Join(outDir, name+".wasm")
		size, sum, err := copyFile(src, dst)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%-20s %8d  sha256:%s\n", name+".wasm", size, sum)
	}
	return nil
}

// parseMembers extracts the crate names from the `members = [...]` entry of
// the [workspace] table. It is a plain string scan, not a TOML parser: it
// expects double-quoted names and no `]` inside them, which is all the
// fixture workspace uses. The list may span several lines.
func parseMembers(manifest string) ([]string, error) {
	ws := strings.Index(manifest, "[workspace]")
	if ws < 0 {
		return nil, errors.New("no [workspace] table")
	}
	section := manifest[ws+len("[workspace]"):]
	if next := strings.Index(section, "\n["); next >= 0 {
		section = section[:next]
	}

	list, found := "", false
	offset := 0
	for line := range strings.Lines(section) {
		if key, _, ok := strings.Cut(line, "="); ok && strings.TrimSpace(key) == "members" {
			rest := section[offset+len(key)+1:]
			open, end := strings.Index(rest, "["), strings.Index(rest, "]")
			if open < 0 || end < open {
				return nil, errors.New("malformed workspace members list")
			}
			list, found = rest[open+1:end], true
			break
		}
		offset += len(line)
	}
	if !found {
		return nil, errors.New("no members in [workspace]")
	}

	var members []string
	for item := range strings.SplitSeq(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue // trailing comma
		}
		name := strings.Trim(item, `"`)
		if name == item || name == "" || strings.ContainsAny(name, `"/\`) {
			return nil, fmt.Errorf("unsupported workspace member %s", item)
		}
		members = append(members, name)
	}
	if len(members) == 0 {
		return nil, errors.New("empty workspace members list")
	}
	return members, nil
}

// copyFile copies src to dst and returns the size and hex sha256 of the data.
func copyFile(src, dst string) (int, string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return 0, "", fmt.Errorf("read build output: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return 0, "", fmt.Errorf("write fixture: %w", err)
	}
	sum := sha256.Sum256(data)
	return len(data), hex.EncodeToString(sum[:]), nil
}
