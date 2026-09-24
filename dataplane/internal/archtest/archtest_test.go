// Package archtest enforces the data plane's package boundaries. The gateway
// and the executor will become separate processes; the only thing they may
// share is the execproto contract. This test is the fitness function that
// keeps the future split mechanical.
package archtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"testing"
)

const internalPrefix = "github.com/kudesn1k1/WasmHooks/dataplane/internal/"

// rule restricts which internal packages a package may depend on,
// transitively. deny lists forbidden packages; only, when non-nil, lists the
// sole allowed ones (an empty non-nil slice allows none).
type rule struct {
	deny []string
	only []string
}

var rules = map[string]rule{
	"gateway":          {deny: []string{"executor", "pool", "sandbox", "sandbox/extismrt", "sandbox/wasminfo", "modstore"}},
	"gateway/httpapi":  {deny: []string{"executor", "pool", "sandbox", "sandbox/extismrt", "sandbox/wasminfo", "modstore"}},
	"executor":         {deny: []string{"gateway", "gateway/httpapi"}},
	"execproto":        {only: []string{}},
	"sandbox":          {only: []string{}},
	"sandbox/extismrt": {only: []string{"sandbox", "sandbox/wasminfo"}},
	"sandbox/wasminfo": {only: []string{"sandbox"}},
	"pool":             {only: []string{"sandbox"}},
	"config":           {only: []string{}},
	"schema":           {only: []string{}},
	"modstore":         {only: []string{}},
	"observe":          {only: []string{}},
}

// exempt packages are test helpers and this package itself.
var exempt = []string{"archtest", "sandbox/sandboxtest"}

type goPackage struct {
	ImportPath string
	Deps       []string
}

func listPackages(t *testing.T) map[string][]string {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = "../.."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	pkgs := map[string][]string{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p goPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		name, ok := strings.CutPrefix(p.ImportPath, internalPrefix)
		if !ok {
			continue
		}
		var deps []string
		for _, d := range p.Deps {
			if dn, ok := strings.CutPrefix(d, internalPrefix); ok {
				deps = append(deps, dn)
			}
		}
		pkgs[name] = deps
	}
	if len(pkgs) == 0 {
		t.Fatal("go list returned no internal packages")
	}
	return pkgs
}

func TestPackageBoundaries(t *testing.T) {
	for pkg, deps := range listPackages(t) {
		r, ok := rules[pkg]
		if !ok {
			continue
		}
		for _, dep := range deps {
			if slices.Contains(r.deny, dep) || (r.only != nil && !slices.Contains(r.only, dep)) {
				t.Errorf("internal/%s must not depend on internal/%s", pkg, dep)
			}
		}
	}
}

func TestRulesCoverAllPackages(t *testing.T) {
	var missing []string
	for pkg := range listPackages(t) {
		if _, ok := rules[pkg]; !ok && !slices.Contains(exempt, pkg) {
			missing = append(missing, pkg)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("add a boundary rule for: %s", strings.Join(missing, ", "))
	}
}
