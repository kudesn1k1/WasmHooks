// Command demo proves the Milestone 0 data plane end to end: it builds and
// starts the data plane from examples/demo/snapshot.json, drives the
// scenario from spec M0 §2 and §14 over HTTP and prints a table of
// step, tenant, expected outcome, actual outcome, HTTP status, latency and
// PASS/FAIL. It exits 0 only if every step passed, so it doubles as a smoke
// test.
//
// Usage (from dataplane/):
//
//	go run ./cmd/demo
//
// With -target it drives an already-running data plane instead of building
// and starting its own (spec M0 §14).
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
)

// demoAPIKey is the bearer token examples/demo/snapshot.json stores the
// SHA-256 of.
const demoAPIKey = "whk_demo_0123456789"

const hookName = "checkout.discount"

// timeoutLatencyBoundMS is the maximum latency step 3 (infinite-loop
// timeout) may take: hook.timeout_ms (50) plus a generous cushion for
// scheduling and the gateway's own overhead. It is a smoke-test bound, not
// an SLO.
const timeoutLatencyBoundMS = 50 + 150

// noisyNeighborToleranceMS: step 5 fails only if merchant-a's max latency
// under load exceeds its baseline max by more than this.
const noisyNeighborToleranceMS = 50

func main() {
	passed, err := run(context.Background(), os.Args[1:], os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
	if !passed {
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) (passed bool, err error) {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	target := fs.String("target", "", "base URL of an already-running data plane (skip building and starting one)")
	apiKey := fs.String("api-key", demoAPIKey, "bearer token for the invoke API")
	if err := fs.Parse(args); err != nil {
		return false, err
	}

	base := *target
	logs := &syncBuffer{}
	if base == "" {
		var stop func()
		base, stop, err = startDataPlane(ctx, stdout, logs)
		if err != nil {
			return false, err
		}
		defer stop()
	}

	client := &httpClient{http: &http.Client{Timeout: 5 * time.Second}, base: base}
	steps := runScenario(ctx, client, *apiKey)

	printTable(stdout, steps)
	nPass := 0
	for _, s := range steps {
		if s.pass {
			nPass++
		}
	}
	fmt.Fprintf(stdout, "\n%d/%d PASS\n", nPass, len(steps))
	if nPass != len(steps) {
		fmt.Fprintln(stdout, "\n--- data plane stderr log (for diagnosing the FAILs above) ---")
		fmt.Fprintln(stdout, logs.String())
	}
	return nPass == len(steps), nil
}

// --- starting the data plane ---------------------------------------------

// exeSuffix is the platform executable extension; Windows requires ".exe".
var exeSuffix = func() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}()

// startDataPlane builds ./cmd/dataplane, prepares a modules directory of
// <hash>.wasm fixtures, starts the binary against examples/demo/snapshot.json
// and waits for /readyz. It returns the base URL to invoke and a stop
// function that shuts the child process down cleanly.
func startDataPlane(ctx context.Context, stdout io.Writer, logs *syncBuffer) (base string, stop func(), err error) {
	dataplaneRoot, snapshotPath, fixturesDir := demoPaths()

	tmp, err := os.MkdirTemp("", "wasmhooks-demo-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanupTmp := func() { os.RemoveAll(tmp) }

	exePath := filepath.Join(tmp, "dataplane"+exeSuffix)
	fmt.Fprintf(stdout, "building data plane -> %s\n", exePath)
	build := exec.Command("go", "build", "-o", exePath, "./cmd/dataplane")
	build.Dir = dataplaneRoot
	var buildOut bytes.Buffer
	build.Stdout = &buildOut
	build.Stderr = &buildOut
	if err := build.Run(); err != nil {
		cleanupTmp()
		return "", nil, fmt.Errorf("go build ./cmd/dataplane: %w\n%s", err, buildOut.String())
	}

	modulesDir := filepath.Join(tmp, "modules")
	if err := os.Mkdir(modulesDir, 0o755); err != nil {
		cleanupTmp()
		return "", nil, fmt.Errorf("create modules dir: %w", err)
	}
	fixtureHashes, err := prepareModules(fixturesDir, modulesDir)
	if err != nil {
		cleanupTmp()
		return "", nil, err
	}
	if err := verifySnapshotHashes(snapshotPath, fixtureHashes); err != nil {
		cleanupTmp()
		return "", nil, err
	}

	cmd := exec.Command(exePath,
		"-listen", "127.0.0.1:0",
		"-snapshot", snapshotPath,
		"-modules-dir", modulesDir,
		"-log-level", "info",
	)
	cmd.Stderr = logs
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cleanupTmp()
		return "", nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cleanupTmp()
		return "", nil, fmt.Errorf("start data plane: %w", err)
	}

	stopChild := func() {
		stopProcess(cmd)
		cleanupTmp()
	}

	addr, err := readListenLine(stdoutPipe)
	if err != nil {
		stopChild()
		return "", nil, fmt.Errorf("%w\nstderr:\n%s", err, logs.String())
	}
	go io.Copy(io.Discard, stdoutPipe)
	base = "http://" + addr

	if err := waitReady(ctx, base, 10*time.Second); err != nil {
		stopChild()
		return "", nil, fmt.Errorf("%w\nstderr:\n%s", err, logs.String())
	}
	fmt.Fprintf(stdout, "data plane ready at %s\n\n", base)
	return base, stopChild, nil
}

// demoPaths locates the demo's fixed inputs relative to this source file, so
// the demo runs correctly regardless of the caller's working directory.
func demoPaths() (dataplaneRoot, snapshotPath, fixturesDir string) {
	_, thisFile, _, _ := runtime.Caller(0)
	demoDir := filepath.Dir(thisFile)
	dataplaneRoot = filepath.Join(demoDir, "..", "..")
	snapshotPath = filepath.Join(dataplaneRoot, "..", "examples", "demo", "snapshot.json")
	fixturesDir = filepath.Join(dataplaneRoot, "testdata", "wasm")
	return
}

// prepareModules copies every *.wasm fixture in fixturesDir into modulesDir
// under its content hash (dataplane's on-disk module addressing scheme). It
// returns the set of hashes now present in modulesDir.
func prepareModules(fixturesDir, modulesDir string) (map[string]bool, error) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		return nil, fmt.Errorf("read fixtures dir %s: %w", fixturesDir, err)
	}
	hashes := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".wasm") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(fixturesDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read fixture %s: %w", e.Name(), err)
		}
		hash := modstore.HashOf(data)
		hexDigest := strings.TrimPrefix(hash, "sha256:")
		if err := os.WriteFile(filepath.Join(modulesDir, hexDigest+".wasm"), data, 0o644); err != nil {
			return nil, fmt.Errorf("write module %s: %w", hexDigest, err)
		}
		hashes[hash] = true
	}
	return hashes, nil
}

// verifySnapshotHashes parses examples/demo/snapshot.json and checks that
// every binding's module_hash matches a fixture actually present in
// fixtureHashes. This is what makes the demo fail loudly and specifically if
// the Rust fixtures are ever rebuilt (their hash changes) without updating
// the committed snapshot.
func verifySnapshotHashes(snapshotPath string, fixtureHashes map[string]bool) error {
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", snapshotPath, err)
	}
	snap, err := config.Parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", snapshotPath, err)
	}
	for _, b := range snap.Bindings {
		if !fixtureHashes[b.ModuleHash] {
			return fmt.Errorf(
				"%s: binding %s/%s references module_hash %s, which matches no fixture in testdata/wasm; "+
					"fixtures were rebuilt; update examples/demo/snapshot.json",
				snapshotPath, b.TenantID, b.Hook, b.ModuleHash)
		}
	}
	return nil
}

func readListenLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("no \"listening on\" line from data plane: %w", err)
	}
	addr, ok := strings.CutPrefix(strings.TrimSpace(line), "listening on ")
	if !ok {
		return "", fmt.Errorf("unexpected first line from data plane: %q", line)
	}
	return addr, nil
}

func waitReady(ctx context.Context, base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/readyz", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("data plane never became ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stopProcess stops cmd's child process the platform-appropriate way and
// reaps it. On Windows, Process.Kill is the only reliable option; elsewhere
// SIGTERM is given 5s to let the server drain in-flight requests before
// Process.Kill.
func stopProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	waitDone := make(chan struct{})
	go func() {
		cmd.Wait()
		close(waitDone)
	}()
	if runtime.GOOS == "windows" {
		cmd.Process.Kill()
		<-waitDone
		return
	}
	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-waitDone:
		return
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		<-waitDone
	}
}

// syncBuffer is an io.Writer safe for concurrent use, so the data plane's
// stderr can be captured by the goroutine os/exec runs for the pipe while
// the main goroutine reads it after a step fails.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// --- HTTP client -----------------------------------------------------------

type httpClient struct {
	http *http.Client
	base string
}

type invokeResult struct {
	status  int
	outcome string
	body    map[string]any
	dur     time.Duration
	err     error
}

func (c *httpClient) invoke(ctx context.Context, tenant, payload, apiKey string) invokeResult {
	start := time.Now()
	reqBody := fmt.Sprintf(`{"tenant_id":%q,"payload":%s}`, tenant, payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/hooks/"+hookName+"/invoke", strings.NewReader(reqBody))
	if err != nil {
		return invokeResult{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	dur := time.Since(start)
	if err != nil {
		return invokeResult{dur: dur, err: err}
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	return invokeResult{
		status:  resp.StatusCode,
		outcome: resp.Header.Get("X-Hook-Outcome"),
		body:    body,
		dur:     dur,
	}
}

// --- scenario ---------------------------------------------------------------

const (
	loyalPayload   = `{"cart_total":9999,"customer":{"id":"c1","lifetime_spend":1500}}`
	newPayload     = `{"cart_total":10,"customer":{"id":"c2","lifetime_spend":10}}`
	invalidPayload = `{"cart_total":10}`
)

type step struct {
	n        int
	tenant   string
	expected string
	got      string
	status   int
	latency  string
	pass     bool
}

func runScenario(ctx context.Context, c *httpClient, apiKey string) []step {
	var steps []step
	add := func(n int, tenant, expected, got string, status int, dur time.Duration, pass bool) {
		steps = append(steps, step{n: n, tenant: tenant, expected: expected, got: got, status: status, latency: fmtMS(dur), pass: pass})
	}

	// Step 1: merchant-a, loyal customer -> ok, discount_percent 10.
	r := c.invoke(ctx, "merchant-a", loyalPayload, apiKey)
	discount, _ := numberField(r.body, "result", "discount_percent")
	pass := r.status == 200 && r.outcome == "ok" && discount == 10
	add(1, "merchant-a", "ok, discount_percent=10", fmt.Sprintf("%s, discount_percent=%v", orErr(r), discount), r.status, r.dur, pass)

	// Step 2: merchant-a, new customer -> ok, discount_percent 0.
	r = c.invoke(ctx, "merchant-a", newPayload, apiKey)
	discount, _ = numberField(r.body, "result", "discount_percent")
	pass = r.status == 200 && r.outcome == "ok" && discount == 0
	add(2, "merchant-a", "ok, discount_percent=0", fmt.Sprintf("%s, discount_percent=%v", orErr(r), discount), r.status, r.dur, pass)

	// Step 3: merchant-b, infinite loop -> timeout, bounded latency.
	r = c.invoke(ctx, "merchant-b", loyalPayload, apiKey)
	withinBound := r.dur <= timeoutLatencyBoundMS*time.Millisecond
	pass = r.status == 200 && r.outcome == "timeout" && withinBound
	add(3, "merchant-b", fmt.Sprintf("timeout, <=%dms", timeoutLatencyBoundMS), orErr(r), r.status, r.dur, pass)

	// Step 4: baseline latency of 20 sequential merchant-a calls.
	baseline, baseOK := latencySample(ctx, c, apiKey, "merchant-a", loyalPayload, 20)
	pass = baseOK
	steps = append(steps, step{
		n: 4, tenant: "merchant-a (x20)",
		expected: "20/20 ok",
		got:      fmt.Sprintf("%d/20 ok, p50=%s max=%s", baseline.okCount, fmtMS(baseline.p50), fmtMS(baseline.max)),
		status:   200, latency: fmtMS(baseline.max), pass: pass,
	})

	// Step 5: merchant-a under load from a hung merchant-b, compared to baseline.
	loaded, loadedOK := latencyUnderNoise(ctx, c, apiKey)
	toleranceOK := loaded.max <= baseline.max+noisyNeighborToleranceMS*time.Millisecond
	pass = loadedOK && toleranceOK
	steps = append(steps, step{
		n: 5, tenant: "merchant-a (x20) + merchant-b hung",
		expected: fmt.Sprintf("20/20 ok, max<=baseline+%dms (%s)", noisyNeighborToleranceMS, fmtMS(baseline.max+noisyNeighborToleranceMS*time.Millisecond)),
		got:      fmt.Sprintf("%d/20 ok, p50=%s max=%s", loaded.okCount, fmtMS(loaded.p50), fmtMS(loaded.max)),
		status:   200, latency: fmtMS(loaded.max), pass: pass,
	})

	// Step 6: merchant-c, memory bomb -> handler_error.
	r = c.invoke(ctx, "merchant-c", loyalPayload, apiKey)
	pass = r.status == 200 && r.outcome == "handler_error"
	add(6, "merchant-c", "handler_error", orErr(r), r.status, r.dur, pass)

	// Step 7: merchant-z, unregistered -> no_handler.
	r = c.invoke(ctx, "merchant-z", loyalPayload, apiKey)
	pass = r.status == 200 && r.outcome == "no_handler"
	add(7, "merchant-z", "no_handler", orErr(r), r.status, r.dur, pass)

	// Step 8: payload without "customer" -> HTTP 400.
	r = c.invoke(ctx, "merchant-a", invalidPayload, apiKey)
	pass = r.status == 400
	add(8, "merchant-a", "HTTP 400", fmt.Sprintf("HTTP %d", r.status), r.status, r.dur, pass)

	// Step 9: wrong API key -> HTTP 401.
	r = c.invoke(ctx, "merchant-a", loyalPayload, "wrong-key")
	pass = r.status == 401
	add(9, "merchant-a", "HTTP 401", fmt.Sprintf("HTTP %d", r.status), r.status, r.dur, pass)

	return steps
}

func orErr(r invokeResult) string {
	if r.err != nil {
		return "error: " + r.err.Error()
	}
	if r.outcome == "" {
		return fmt.Sprintf("HTTP %d", r.status)
	}
	return r.outcome
}

func numberField(body map[string]any, keys ...string) (float64, bool) {
	var cur any = body
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur, ok = m[k]
		if !ok {
			return 0, false
		}
	}
	n, ok := cur.(float64)
	return n, ok
}

type latencyStats struct {
	p50, max time.Duration
	okCount  int
}

// latencySample makes n sequential invocations and reports p50/max latency
// and how many returned outcome=ok.
func latencySample(ctx context.Context, c *httpClient, apiKey, tenant, payload string, n int) (latencyStats, bool) {
	durs := make([]time.Duration, 0, n)
	okCount := 0
	for range n {
		r := c.invoke(ctx, tenant, payload, apiKey)
		durs = append(durs, r.dur)
		if r.status == 200 && r.outcome == "ok" {
			okCount++
		}
	}
	return statsOf(durs, okCount), okCount == n
}

// latencyUnderNoise keeps merchant-b's pool saturated with hung
// infinite-loop calls in the background, then measures 20 sequential
// merchant-a calls made while that background load is running.
func latencyUnderNoise(ctx context.Context, c *httpClient, apiKey string) (latencyStats, bool) {
	const noisyWorkers = 4 // merchant-b's concurrency_limit in the demo snapshot
	noiseCtx, cancelNoise := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for range noisyWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for noiseCtx.Err() == nil {
				c.invoke(noiseCtx, "merchant-b", loyalPayload, apiKey)
			}
		}()
	}
	// Give the noise workers a moment to occupy merchant-b's pool before
	// measuring merchant-a.
	time.Sleep(20 * time.Millisecond)

	stats, ok := latencySample(ctx, c, apiKey, "merchant-a", loyalPayload, 20)

	cancelNoise()
	wg.Wait()
	return stats, ok
}

func statsOf(durs []time.Duration, okCount int) latencyStats {
	sorted := slices.Clone(durs)
	slices.Sort(sorted)
	stats := latencyStats{okCount: okCount}
	if len(sorted) == 0 {
		return stats
	}
	stats.p50 = sorted[len(sorted)/2]
	stats.max = sorted[len(sorted)-1]
	return stats
}

func fmtMS(d time.Duration) string {
	return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000.0)
}

func printTable(w io.Writer, steps []step) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tTENANT\tEXPECTED\tGOT\tHTTP\tLATENCY\tRESULT")
	for _, s := range steps {
		result := "PASS"
		if !s.pass {
			result = "FAIL"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\t%s\n", s.n, s.tenant, s.expected, s.got, s.status, s.latency, result)
	}
	tw.Flush()
}
