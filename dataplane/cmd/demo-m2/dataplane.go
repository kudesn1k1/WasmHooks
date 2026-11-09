package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
)

// moduleHashes are the content hashes of the two fixtures the demo binds.
type moduleHashes struct{ discount, loop string }

var modules moduleHashes

var exeSuffix = func() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}()

// prepare builds ./cmd/dataplane into dir and copies the discount and
// infinite-loop fixtures into dir/modules under their content hashes.
func prepare(dir string) (exe, modulesDir string, err error) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")

	exe = filepath.Join(dir, "dataplane"+exeSuffix)
	build := exec.Command("go", "build", "-o", exe, "./cmd/dataplane")
	build.Dir = root
	var out bytes.Buffer
	build.Stdout, build.Stderr = &out, &out
	if err := build.Run(); err != nil {
		return "", "", fmt.Errorf("go build ./cmd/dataplane: %w\n%s", err, out.String())
	}

	modulesDir = filepath.Join(dir, "modules")
	if err := os.Mkdir(modulesDir, 0o755); err != nil {
		return "", "", err
	}
	for name, dst := range map[string]*string{"discount.wasm": &modules.discount, "infinite-loop.wasm": &modules.loop} {
		data, err := os.ReadFile(filepath.Join(root, "testdata", "wasm", name))
		if err != nil {
			return "", "", err
		}
		*dst = modstore.HashOf(data)
		hexDigest := strings.TrimPrefix(*dst, "sha256:")
		if err := os.WriteFile(filepath.Join(modulesDir, hexDigest+".wasm"), data, 0o644); err != nil {
			return "", "", err
		}
	}
	return exe, modulesDir, nil
}

// dataPlane is a running data plane process.
type dataPlane struct {
	cmd  *exec.Cmd
	base string
	done chan struct{}
}

// startDataPlane runs the binary against the stand-in control plane with
// GOMAXPROCS procs and extra flags. Every invocation the data plane logs is
// passed to onInvocation.
func startDataPlane(exe, modulesDir string, cp *controlPlane, procs int, extra []string, onInvocation func(invocationLog)) (*dataPlane, error) {
	args := append([]string{
		"-listen", "127.0.0.1:0",
		"-internal-listen", "127.0.0.1:0",
		"-control-plane-url", cp.url,
		"-modules-dir", modulesDir,
		"-log-level", "info",
	}, extra...)
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "WASMHOOKS_INTERNAL_TOKEN="+cp.token, fmt.Sprintf("GOMAXPROCS=%d", procs))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start data plane: %w", err)
	}
	dp := &dataPlane{cmd: cmd, done: make(chan struct{})}
	go func() {
		readInvocations(stderr, onInvocation)
		close(dp.done)
	}()

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		dp.stop()
		return nil, fmt.Errorf("no \"listening on\" line from data plane: %w", err)
	}
	go io.Copy(io.Discard, stdout)
	dp.base = "http://" + strings.TrimSpace(strings.TrimPrefix(line, "listening on "))
	if err := waitReady(dp.base, 15*time.Second); err != nil {
		dp.stop()
		return nil, err
	}
	return dp, nil
}

func (dp *dataPlane) pid() int { return dp.cmd.Process.Pid }

// stop kills the process: the demo never needs a graceful drain.
func (dp *dataPlane) stop() {
	dp.cmd.Process.Kill()
	dp.cmd.Wait()
	<-dp.done
}

func waitReady(base string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(base + "/readyz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("data plane never became ready")
}

// invocationLog is the part of the data plane's per-invocation log line
// (observe.SlogSink) the demo reads: Exec is the time spent inside the
// script, so for a timeout Exec minus the hook limit is how late the
// deadline watcher stopped it.
type invocationLog struct {
	Msg      string        `json:"msg"`
	TenantID string        `json:"tenant_id"`
	Outcome  string        `json:"outcome"`
	Reason   string        `json:"reason"`
	Exec     time.Duration `json:"exec"`
}

func readInvocations(r io.Reader, on func(invocationLog)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var l invocationLog
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Msg == "invocation" {
			on(l)
		}
	}
}
