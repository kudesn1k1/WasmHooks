package modstore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore/storetest"
)

// Store must be satisfied by *FS.
var _ modstore.Store = (*modstore.FS)(nil)

func TestFS_Behaviour(t *testing.T) {
	storetest.Run(t, func(t *testing.T, objects map[string][]byte) modstore.Store {
		dir := t.TempDir()
		for hash, content := range objects {
			hexDigest, err := modstore.ParseHash(hash)
			if err != nil {
				t.Fatalf("ParseHash(%q): %v", hash, err)
			}
			if err := os.WriteFile(filepath.Join(dir, hexDigest+".wasm"), content, 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
		}
		return modstore.NewFS(dir)
	})
}
