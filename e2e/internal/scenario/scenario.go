// Package scenario drives the Milestone 1 end-to-end scenario (spec M1 §8.2)
// against a running compose. It proves three claims: a config change made
// through the control plane reaches the data plane without a restart, the
// data plane keeps serving its last snapshot while the control plane is
// down, and (should tier) a module seeded into MinIO runs and module
// validation reports the right verdicts.
//
// Every run uses fresh names (hook, tenant, API key), so the scenario passes
// again on the same compose without `docker compose down -v`.
package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// Config says where the compose and its two public endpoints are.
type Config struct {
	ComposeDir    string // directory with compose.yaml
	ControlPlane  string // http://localhost:8000
	DataPlane     string // http://localhost:8080
	IncludeShould bool   // run steps 5-7 (seed and validate)
}

// Step is one row of the scenario table.
type Step struct {
	N        string // "1".."7"
	Name     string
	Expected string
	Actual   string
	Elapsed  time.Duration // wall time of the whole step
	Pass     bool
}

// Limits of the scenario. The bounds that PASS depends on come from spec M1
// §8.2 and the task brief; the rest only stop a broken run from hanging.
const (
	dataPlaneReadyLimit    = 30 * time.Second // step 0: may be in backoff up to 10 s on a fresh compose
	keyPropagationLimit    = 3 * time.Second  // step 1
	propagationBound       = 1 * time.Second  // step 2 PASS bound
	propagationLimit       = 10 * time.Second // step 2 gives up polling
	callsWhileDown         = 20               // step 3
	controlPlaneReadyLimit = 30 * time.Second // step 4
	pickupLimit            = 15 * time.Second // step 4: data plane backoff is at most 10 s
	seededRunLimit         = 10 * time.Second // step 5

	httpTimeout = 5 * time.Second
)

// Payloads of checkout.discount (examples/demo/snapshot.json). The second
// carries the field step 4 makes required.
const (
	loyalPayload         = `{"cart_total":9999,"customer":{"id":"c1","lifetime_spend":1500}}`
	loyalPayloadCurrency = `{"cart_total":9999,"currency":"USD","customer":{"id":"c1","lifetime_spend":1500}}`
)

const controlPlaneService = "control-plane"

// result is what a step reports; stop means the system is unusable for the
// steps after it.
type result struct {
	actual string
	pass   bool
	stop   bool
}

type stepDef struct {
	n, name, expected string
	should            bool
	run               func(context.Context) result
}

type runner struct {
	cfg  Config
	http *http.Client

	hook, tenant, keyName string
	demo                  hookSpec // checkout.discount from examples/demo/snapshot.json

	apiKey string // created in step 1

	// controlPlaneStopped is true between step 3 stopping the control plane
	// and step 4 starting it again; Run restarts it if a run ends in between.
	controlPlaneStopped bool
}

// Run executes the M1 scenario from spec M1 §8.2 and returns every step,
// stopping early only when a step leaves the system unusable for the next;
// steps after that are returned as failed with "not run". It returns an
// error only if the scenario cannot start (no compose.yaml, data plane not
// ready within 30 s) or ctx is cancelled.
func Run(ctx context.Context, cfg Config) ([]Step, error) {
	if _, err := os.Stat(filepath.Join(cfg.ComposeDir, "compose.yaml")); err != nil {
		abs, _ := filepath.Abs(cfg.ComposeDir)
		return nil, fmt.Errorf("no compose.yaml in %s: %w", abs, err)
	}
	demo, err := loadDemoHook(filepath.Join(cfg.ComposeDir, "examples", "demo", "snapshot.json"))
	if err != nil {
		return nil, err
	}
	// Lowercase base36 with a letter first: valid in a hook name segment.
	suffix := "r" + strconv.FormatInt(time.Now().Unix(), 36)
	r := &runner{
		cfg:     cfg,
		http:    &http.Client{Timeout: httpTimeout},
		hook:    "checkout.discount_" + suffix,
		tenant:  "merchant-a-" + suffix,
		keyName: "e2e-" + suffix,
		demo:    demo,
	}
	defer r.restoreControlPlane(ctx)

	// Step 0, a precondition rather than a row: on a fresh compose the data
	// plane starts before the control plane and is not ready until its first
	// snapshot.
	if _, err := waitOK(ctx, r.http, cfg.DataPlane+"/readyz", dataPlaneReadyLimit); err != nil {
		return nil, fmt.Errorf("data plane not ready: %w", err)
	}

	defs := []stepDef{
		{"1", "API key reaches data plane", "401 -> 404 within 3s", false, r.keyReachesDataPlane},
		{"2", "new hook reaches data plane", "404 -> no_handler, <=1s", false, r.hookReachesDataPlane},
		{"3", "control plane stopped", "20/20 no_handler, readyz 200", false, r.servesWhileControlPlaneDown},
		{"4", "control plane back, hook changed", "old payload -> 400 within 15s", false, r.picksUpChangeAfterOutage},
		{"5", "seeded module runs", "ok, discount_percent=10", true, r.seededModuleRuns},
		{"6", "validate http-call.wasm", "ok=false, imports failed", true, r.validateHTTPCall},
		{"7", "validate discount.wasm", "ok=true", true, r.validateDiscount},
	}
	var steps []Step
	stoppedAt := ""
	for _, d := range defs {
		if d.should && !cfg.IncludeShould {
			continue
		}
		s := Step{N: d.n, Name: d.name, Expected: d.expected}
		if stoppedAt != "" {
			s.Actual = "not run: step " + stoppedAt + " failed"
			steps = append(steps, s)
			continue
		}
		start := time.Now()
		res := d.run(ctx)
		s.Elapsed = time.Since(start)
		s.Actual, s.Pass = res.actual, res.pass
		steps = append(steps, s)
		if err := ctx.Err(); err != nil {
			return steps, err
		}
		if res.stop {
			stoppedAt = d.n
		}
	}
	return steps, nil
}

// restoreControlPlane starts the control plane again if the run ended while
// it was stopped, so a failed or interrupted run never leaves the compose
// half down for the next run.
func (r *runner) restoreControlPlane(ctx context.Context) {
	if !r.controlPlaneStopped {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if err := startService(ctx, r.cfg.ComposeDir, controlPlaneService); err != nil {
		fmt.Fprintln(os.Stderr, "scenario: could not start the control plane again:", err)
	}
}

// --- steps -----------------------------------------------------------------

// Step 1: a key created with the CLI reaches the data plane through the
// snapshot. The hook does not exist yet, so the first answer that is not 401
// must be 404.
func (r *runner) keyReachesDataPlane(ctx context.Context) result {
	out, err := controlPlaneCLI(ctx, r.cfg.ComposeDir, "apikey", "create", "--name", r.keyName)
	if err != nil {
		return result{actual: err.Error(), stop: true}
	}
	r.apiKey = strings.TrimSpace(out)
	if r.apiKey == "" || strings.ContainsAny(r.apiKey, " \n") {
		return result{actual: fmt.Sprintf("apikey create printed %q, want one token", out), stop: true}
	}
	start := time.Now()
	unauthorized := 0
	last, _, done := r.pollInvoke(ctx, r.hook, r.tenant, loyalPayload, 50*time.Millisecond, keyPropagationLimit,
		func(res invokeResult) bool {
			if res.status == http.StatusUnauthorized {
				unauthorized++
			}
			return res.err == nil && res.status != http.StatusUnauthorized
		})
	took := time.Since(start)
	if !done {
		return result{actual: fmt.Sprintf("still %s after %s", last, fmtDur(took)), stop: true}
	}
	return result{
		actual: keyActual(unauthorized, last, took),
		pass:   last.status == http.StatusNotFound,
	}
}

// keyActual describes step 1. On a quiet compose the key often reaches the
// data plane while `docker compose exec` is still returning, so no 401 is
// seen at all; the step then shows that rather than a transition.
func keyActual(unauthorized int, last invokeResult, took time.Duration) string {
	if unauthorized == 0 {
		return fmt.Sprintf("%s on the first call (key already there)", last)
	}
	return fmt.Sprintf("401 x%d -> %s in %s", unauthorized, last, fmtDur(took))
}

// Step 2: a hook created through the operator API reaches the data plane
// without a restart. Propagation is timed from the POST's 201, the moment
// the change is committed.
func (r *runner) hookReachesDataPlane(ctx context.Context) result {
	body := hookCreate{Name: r.hook, hookSpec: r.demo}
	sent := time.Now()
	status, resp, err := r.controlPlaneRequest(ctx, http.MethodPost, "/api/v1/hooks", body)
	postTook := time.Since(sent)
	if err != nil {
		return result{actual: "POST /api/v1/hooks: " + err.Error(), stop: true}
	}
	if status != http.StatusCreated {
		return result{actual: fmt.Sprintf("POST /api/v1/hooks: HTTP %d %s", status, oneLine(resp)), stop: true}
	}
	committed := time.Now()
	// 503 is not a changed answer: it only says the data plane could not
	// serve this one call.
	last, _, done := r.pollInvoke(ctx, r.hook, r.tenant, loyalPayload, 20*time.Millisecond, propagationLimit,
		func(res invokeResult) bool {
			return res.err == nil && res.status != http.StatusNotFound && res.status != http.StatusServiceUnavailable
		})
	took := time.Since(committed)
	if !done {
		return result{actual: fmt.Sprintf("still %s after %s", last, fmtDur(took))}
	}
	return result{
		actual: fmt.Sprintf("%s after %s (POST %s)", last, fmtDur(took), fmtDur(postTook)),
		pass:   last.status == http.StatusOK && last.outcome == "no_handler" && took <= propagationBound,
	}
}

// Step 3: with the control plane stopped, calls are served from the last
// snapshot and the data plane stays ready.
func (r *runner) servesWhileControlPlaneDown(ctx context.Context) result {
	start := time.Now()
	r.controlPlaneStopped = true // set first: a failed stop may still have stopped it
	if err := stopService(ctx, r.cfg.ComposeDir, controlPlaneService); err != nil {
		return result{actual: err.Error()}
	}
	stopTook := time.Since(start)

	controlPlaneDown := true
	if resp, err := r.http.Get(r.cfg.ControlPlane + "/readyz"); err == nil {
		resp.Body.Close()
		controlPlaneDown = resp.StatusCode != http.StatusOK
	}

	served := 0
	var odd invokeResult // first call that was not no_handler, for the report
	for range callsWhileDown {
		res := r.invoke(ctx, r.hook, r.tenant, loyalPayload)
		if res.err == nil && res.status == http.StatusOK && res.outcome == "no_handler" {
			served++
		} else if odd.status == 0 && odd.err == nil {
			odd = res
		}
	}
	ready := 0
	if resp, err := r.http.Get(r.cfg.DataPlane + "/readyz"); err == nil {
		resp.Body.Close()
		ready = resp.StatusCode
	}

	actual := fmt.Sprintf("%d/%d no_handler, readyz %d (stop took %s)", served, callsWhileDown, ready, fmtDur(stopTook))
	if served < callsWhileDown {
		actual += fmt.Sprintf(", got %s", odd)
	}
	if !controlPlaneDown {
		actual += ", but the control plane still answers"
	}
	return result{actual: actual, pass: controlPlaneDown && served == callsWhileDown && ready == http.StatusOK}
}

// Step 4: after the control plane comes back, the next change reaches the
// data plane: a new required field in input_schema turns the old payload
// into a 400. The data plane may be in backoff for up to 10 s, hence 15 s.
func (r *runner) picksUpChangeAfterOutage(ctx context.Context) result {
	start := time.Now()
	if err := startService(ctx, r.cfg.ComposeDir, controlPlaneService); err != nil {
		return result{actual: err.Error(), stop: true}
	}
	r.controlPlaneStopped = false
	if _, err := waitOK(ctx, r.http, r.cfg.ControlPlane+"/readyz", controlPlaneReadyLimit); err != nil {
		return result{actual: "control plane not ready: " + err.Error(), stop: true}
	}
	upTook := time.Since(start)

	status, resp, err := r.controlPlaneRequest(ctx, http.MethodPut, "/api/v1/hooks/"+r.hook, r.demo.withRequiredCurrency())
	if err != nil {
		return result{actual: "PUT /api/v1/hooks: " + err.Error()}
	}
	if status != http.StatusOK {
		return result{actual: fmt.Sprintf("PUT /api/v1/hooks: HTTP %d %s", status, oneLine(resp))}
	}
	changed := time.Now()
	last, _, done := r.pollInvoke(ctx, r.hook, r.tenant, loyalPayload, 100*time.Millisecond, pickupLimit,
		func(res invokeResult) bool { return res.err == nil && res.status == http.StatusBadRequest })
	took := time.Since(changed)
	if !done {
		return result{actual: fmt.Sprintf("still %s %s after the PUT", last, fmtDur(took))}
	}
	return result{
		actual: fmt.Sprintf("%s %s after the PUT (control plane up in %s)", last, fmtDur(took), fmtDur(upTook)),
		pass:   true,
	}
}

// Step 5: the dev seed puts discount.wasm into MinIO and binds it to the
// tenant; the data plane fetches the module by hash and runs it. Until the
// binding arrives the call is no_handler, and while the module is being
// fetched and compiled it may be unavailable, so the step polls.
func (r *runner) seededModuleRuns(ctx context.Context) result {
	out, err := controlPlaneCLI(ctx, r.cfg.ComposeDir,
		"seed-demo", "--wasm", "/fixtures/discount.wasm", "--tenant", r.tenant, "--hook", r.hook)
	if err != nil {
		return result{actual: err.Error()}
	}
	moduleHash := strings.TrimSpace(out)
	start := time.Now()
	last, calls, done := r.pollInvoke(ctx, r.hook, r.tenant, loyalPayloadCurrency, 50*time.Millisecond, seededRunLimit,
		func(res invokeResult) bool {
			return res.err == nil && res.outcome != "no_handler" && res.outcome != "unavailable"
		})
	took := time.Since(start)
	if !done {
		return result{actual: fmt.Sprintf("still %s after %s", last, fmtDur(took))}
	}
	discount, _ := numberField(last.body, "result", "discount_percent")
	ranHash, _ := last.body["module_hash"].(string)
	actual := fmt.Sprintf("%s, discount_percent=%v on call %d, %s", last, discount, calls, fmtDur(took))
	if ranHash != moduleHash {
		actual += fmt.Sprintf(", ran %q but seeded %q", ranHash, moduleHash)
	}
	return result{
		actual: actual,
		pass:   last.status == http.StatusOK && last.outcome == "ok" && discount == 10 && ranHash == moduleHash,
	}
}

// Step 6: a module importing a host function the hook does not allow fails
// the imports check.
func (r *runner) validateHTTPCall(ctx context.Context) result {
	report, err := r.validateModule(ctx, "/fixtures/http-call.wasm")
	if err != nil {
		return result{actual: err.Error()}
	}
	importsFailed := slices.ContainsFunc(report.Checks, func(c check) bool { return c.Name == "imports" && c.failed() })
	return result{actual: report.String(), pass: !report.OK && importsFailed}
}

// Step 7: the discount module passes every check against the changed hook.
func (r *runner) validateDiscount(ctx context.Context) result {
	report, err := r.validateModule(ctx, "/fixtures/discount.wasm")
	if err != nil {
		return result{actual: err.Error()}
	}
	return result{actual: report.String(), pass: report.OK}
}

// --- module validation -----------------------------------------------------

// validationReport is ValidationReport from
// api/dataplane-internal.openapi.yaml.
type validationReport struct {
	OK     bool    `json:"ok"`
	Checks []check `json:"checks"`
}

type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Checks after the first failed one come back as ok=false with detail
// "skipped": they were not run, so they did not fail.
const skippedDetail = "skipped"

func (c check) failed() bool { return !c.OK && c.Detail != skippedDetail }

func (v validationReport) String() string {
	var passed int
	var failed, skipped []string
	for _, c := range v.Checks {
		switch {
		case c.OK:
			passed++
		case c.failed():
			failed = append(failed, c.Name) // details are long; names fit the table
		default:
			skipped = append(skipped, c.Name)
		}
	}
	s := fmt.Sprintf("ok=%t, %d/%d checks ok", v.OK, passed, len(v.Checks))
	if len(failed) > 0 {
		s += ", failed: " + strings.Join(failed, ", ")
	}
	if len(skipped) > 0 {
		s += ", skipped: " + strings.Join(skipped, ", ")
	}
	return s
}

func (r *runner) validateModule(ctx context.Context, path string) (validationReport, error) {
	out, err := controlPlaneCLI(ctx, r.cfg.ComposeDir, "validate-module", path, "--hook", r.hook)
	if err != nil {
		return validationReport{}, err
	}
	var report validationReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		return validationReport{}, fmt.Errorf("validate-module printed %s: %w", oneLine([]byte(out)), err)
	}
	return report, nil
}

// --- hooks -----------------------------------------------------------------

// hookSpec is the body of PUT /api/v1/hooks/{name} (HookSpec in
// api/control-plane.openapi.yaml); the API rejects unknown fields.
type hookSpec struct {
	InputSchema          map[string]any `json:"input_schema"`
	OutputSchema         map[string]any `json:"output_schema"`
	TimeoutMS            int            `json:"timeout_ms"`
	MemoryMaxPages       int            `json:"memory_max_pages"`
	AllowedHostFunctions []string       `json:"allowed_host_functions"`
	AllowedEffectTypes   []string       `json:"allowed_effect_types"`
	SampleInput          map[string]any `json:"sample_input"`
}

// hookCreate is the body of POST /api/v1/hooks: a spec plus the name.
type hookCreate struct {
	Name string `json:"name"`
	hookSpec
}

// loadDemoHook reads checkout.discount from examples/demo/snapshot.json,
// the schemas the M0 demo and the discount module are built around.
func loadDemoHook(path string) (hookSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return hookSpec{}, fmt.Errorf("read demo snapshot: %w", err)
	}
	var snap struct {
		Hooks []struct {
			Name         string         `json:"name"`
			InputSchema  map[string]any `json:"input_schema"`
			OutputSchema map[string]any `json:"output_schema"`
			SampleInput  map[string]any `json:"sample_input"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return hookSpec{}, fmt.Errorf("%s: %w", path, err)
	}
	for _, h := range snap.Hooks {
		if h.Name == "checkout.discount" {
			return hookSpec{
				InputSchema:          h.InputSchema,
				OutputSchema:         h.OutputSchema,
				TimeoutMS:            50,
				MemoryMaxPages:       64,
				AllowedHostFunctions: []string{},
				AllowedEffectTypes:   []string{},
				SampleInput:          h.SampleInput,
			}, nil
		}
	}
	return hookSpec{}, fmt.Errorf("%s: no hook checkout.discount", path)
}

// withRequiredCurrency is the spec with a new required string field
// "currency" in input_schema and in sample_input, so the sample stays valid.
// Everything else, including the allowed lists, stays as it is: the API
// rejects narrowing them.
func (s hookSpec) withRequiredCurrency() hookSpec {
	out := s
	out.InputSchema = deepCopy(s.InputSchema)
	required, _ := out.InputSchema["required"].([]any)
	out.InputSchema["required"] = append(slices.Clone(required), "currency")
	props, _ := out.InputSchema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		out.InputSchema["properties"] = props
	}
	props["currency"] = map[string]any{"type": "string"}
	out.SampleInput = deepCopy(s.SampleInput)
	out.SampleInput["currency"] = "USD"
	return out
}

func deepCopy(m map[string]any) map[string]any {
	data, err := json.Marshal(m)
	if err != nil {
		panic(err) // decoded from JSON, so it always encodes
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return out
}

// --- HTTP ------------------------------------------------------------------

type invokeResult struct {
	status  int
	outcome string // X-Hook-Outcome
	body    map[string]any
	err     error
}

// String is the outcome for 200, 429 and 503, else the HTTP status.
func (res invokeResult) String() string {
	switch {
	case res.err != nil:
		return "error: " + res.err.Error()
	case res.outcome != "":
		return res.outcome
	default:
		return strconv.Itoa(res.status)
	}
}

func (r *runner) invoke(ctx context.Context, hook, tenant, payload string) invokeResult {
	body := fmt.Sprintf(`{"tenant_id":%q,"payload":%s}`, tenant, payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.DataPlane+"/v1/hooks/"+hook+"/invoke", strings.NewReader(body))
	if err != nil {
		return invokeResult{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return invokeResult{err: err}
	}
	defer resp.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded) // problem+json or InvokeResponse; either is fine
	return invokeResult{status: resp.StatusCode, outcome: resp.Header.Get("X-Hook-Outcome"), body: decoded}
}

// pollInvoke calls the hook every interval until done accepts an answer or
// limit passes. It returns the last answer, the number of calls and whether
// done accepted it.
func (r *runner) pollInvoke(ctx context.Context, hook, tenant, payload string, interval, limit time.Duration,
	done func(invokeResult) bool) (last invokeResult, calls int, ok bool) {
	deadline := time.Now().Add(limit)
	for {
		last = r.invoke(ctx, hook, tenant, payload)
		calls++
		if done(last) {
			return last, calls, true
		}
		if time.Now().After(deadline) || sleep(ctx, interval) != nil {
			return last, calls, false
		}
	}
}

// controlPlaneRequest sends body as JSON to the operator API with the key
// from step 1 and returns the status and the response body.
func (r *runner) controlPlaneRequest(ctx context.Context, method, path string, body any) (int, []byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, r.cfg.ControlPlane+path, bytes.NewReader(data))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, respBody, err
}

// waitOK polls url until it answers 200 or limit passes.
func waitOK(ctx context.Context, client *http.Client, url string, limit time.Duration) (time.Duration, error) {
	start := time.Now()
	for {
		last := ""
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return time.Since(start), nil
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		if time.Since(start) > limit {
			return 0, fmt.Errorf("%s not 200 within %s, last: %s", url, limit, last)
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return 0, err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
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

// oneLine squeezes a response body into a short single line for the table.
func oneLine(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

func fmtDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// WriteTable prints the steps as the M0 demo does (dataplane/cmd/demo),
// followed by the PASS count.
func WriteTable(w io.Writer, steps []Step) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tSTEP\tEXPECTED\tACTUAL\tTIME\tRESULT")
	for _, s := range steps {
		verdict := "PASS"
		if !s.Pass {
			verdict = "FAIL"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.N, s.Name, s.Expected, s.Actual, fmtDur(s.Elapsed), verdict)
	}
	tw.Flush()
	fmt.Fprintf(w, "\n%d/%d PASS\n", Passed(steps), len(steps))
}

// Passed counts the steps that passed.
func Passed(steps []Step) int {
	n := 0
	for _, s := range steps {
		if s.Pass {
			n++
		}
	}
	return n
}
