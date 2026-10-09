# Review 2: M1 whole milestone (final checkpoint)

**Scope.** Worktree `D:\Uni\PP26-dp`, branch `m1-dataplane` @ `ce6fcfa`, compared with main `fea2227`.
- PR2 (`61f9601..ce6fcfa`) was reviewed in full from the diff package.
- PR1 code was read where PR2 depends on it: dpclient, devtools, storage, CLI, the hooks slice, and the stub wiring.

**Scope gap you should know about.** Review 1 covered only `dd08f62..e106b77` (Tasks 1–4). Nobody independently reviewed Tasks 5–8 or the fixes (`8fc6f7c..6e24c77`) before this review. I covered the parts of them that touch PR2 or the data-plane contract:
- `hooks/` (T5);
- `dpclient/`, `devtools/`, `storage/` and `cli.py` (T7);
- stub auth wiring (T6).

I skimmed the handoff (T8) only for its T4 contract and did not proofread it.

**Rules followed.** I made no changes in the repos. Experiments ran in a separate copy in the scratchpad (`…/scratchpad/r2`, from `git archive HEAD`) and against the `wasmhooks` compose. `git status` in the worktree is clean afterwards.

**Verdict.**
- DoD: 6 of 7 must items PASS (#5 has one process clause that cannot be checked before the PR exists). #7 is not started, by plan (Task 14). Both should items PASS.
- Findings: 0 Critical, 3 Important, 15 Minor.
- The core claims hold under real failures:
  - a 50 s black-hole of the control plane;
  - a 70 s outage of the control plane;
  - a data-plane restart during the outage;
  - a token mismatch;
  - storage failures against real MinIO.
- The three Important findings:
  - I1: the validation tests flake under CPU load (reproduced). The same mechanism can reject a good module in production.
  - I2: `DataPlaneClient` lets a dropped connection escape as an unmapped exception. The T4 handoff tells the backend developer to catch only the two mapped ones.
  - I3: the control plane accepts hook schemas the data plane cannot compile. That hook is then `unavailable` forever, and Validate blames the module for it.

---

## DoD (spec §2)

| # | Item | Verdict | Evidence |
|---|---|---|---|
| 1 | `control-plane/` foundation, tests on real PG, CI job | **PASS** | `python -m uv run pytest -q`: **179 passed** (44 s). `ruff check`: clean. `ruff format --check`: 70 files formatted. `mypy`: no issues in 50 files. `.github/workflows/ci.yml` has the `control-plane` job (uv sync, ruff, mypy, pytest, openapi drift). |
| 2 | Data plane takes snapshots from the CP, backoff, keeps serving through an outage; M0 `-snapshot` mode unchanged | **PASS** (Minor m1, m2) | Data-plane suite under `go test -race -count=1 ./...`: green in 4 of 5 full runs; the one failure is I1. Experiment B (`docker compose pause control-plane` for 50 s): calls served, `readyz` 200; the long-poll timed out at 40 s and backed off; the next change was picked up **0.43 s** after unpause. Experiment C (`stop` for 70 s): calls served, `readyz` 200, recovered after start. Wrong token: `readyz` 503, invoke 503 `unavailable`, a clear ERROR on every retry. M0 demo (`cd dataplane && go run ./cmd/demo`): **9/9 PASS**. |
| 3 | `docker compose up` brings up PG, MinIO, CP, DP | **PASS** | `docker compose up -d --build --wait control-plane dataplane`: everything healthy in 13.6 s. `docker port wasmhooks-dataplane-1` shows only `8080/tcp -> 127.0.0.1:8080`, so `:8081` is not published. |
| 4 | Demo proves the claims | **PASS** | `cd e2e && go run ./cmd/demo` twice on one compose: **7/7 PASS** both times, exit 0. Propagation 337 ms and 483 ms; control-plane stop 1.86 s and 1.70 s; pickup after restart 2.35 s and 307 ms. See e2e notes m9 and m10. |
| 5 | Console contract: stubs, generated `api/control-plane.openapi.yaml`, CI drift check | **PASS**, one clause unverifiable | `controlplane openapi` diffed against the committed file: `NO_DRIFT`. The CI drift step exists. Stubs are behind `OperatorAuth`/`TenantAuth` (`tenants/router.py:10`, `tenantconsole/router.py:10`). "Frontend developer among the PR reviewers" cannot be checked until the PR exists. |
| 6 | `docs/handoff/backend.md`, M1 section in `TEAM.md` | **PASS** (I2 affects the T4 section) | `backend.md` has 705 lines with §1–7 and T1–T8, each with acceptance criteria (lines 466–673). `TEAM.md:65` has "Milestone 1". The T4 retry contract (lines 559–560) is incomplete; see I2. |
| 7 | Milestone walkthrough, PR composition, delegation report, links in the main spec, CI green on the PRs | **NOT STARTED (by plan, Task 14 after Review 2)** | `grep -c 'D1[6-9]\|D2[0-6]'` on the main spec returns 0. There is no walkthrough file. CI on the PRs cannot be checked before the PRs exist, and I1 threatens it. |
| S1 | `modstore.S3` + Validate on the internal port | **PASS** (I1, I3) | Real MinIO (experiment G, all `ok:false` results are HTTP 200): NoSuchKey → `fetch` failed; tampered object → `fetch` failed with hash mismatch; forbidden import → `imports` failed, `sample_call` skipped; wrong bucket, wrong secret, or MinIO stopped → 503 (the last after 6.2 s of minio-go retries). Internal API: 401 without a token; 413 at 1 MiB; 404 for the path on the public port. |
| S2 | Dev seed; demo shows `ok` from MinIO and the two Validate reports | **PASS** (m5) | Demo steps 5–7 pass, and step 5 checks that `module_hash` equals the seeded hash. |
| St | S7b | not started (by plan) | — |

---

## Critical

None.

## Important

### Verified by experiment

#### I1. Validation tests flake under CPU load, and the same mechanism can reject a good module in production
- **Location:**
  - `dataplane/cmd/dataplane/controlplane_test.go:245` (`"timeout_ms":50`);
  - `dataplane/internal/executor/validate_test.go:30` (`TimeoutMS: 50` in the shared `validateHook`, used by the `discount → ok` case);
  - product side: `internal/executor/validate.go:136` (the sample-call deadline is the hook's `timeout_ms`) and `extismrt/runtime.go:137` (Compile's trial instantiation also runs under `timeout_ms`).
- **Evidence:**
  - My first plain `go test -race -count=1 ./...` failed `TestInternalValidate` with `validate = 200 ok=false`.
  - Instrumented copy, `-race -count=15` while a `go build -race -a` and a `go vet` ran in parallel: 2 of 15 failed, both with `sample_call … "timeout after 50 ms"`.
  - The test is green when the machine is quiet: 10/10 alone, and 3 later full runs.
- **CI scenario:**
  - The `go` job runs `go test -race ./...`. It runs packages in parallel on a shared 4-vCPU runner, alongside the CPU-heavy `executor` and `extismrt` tests.
  - A 50 ms deadline under the race detector is a coin toss there, so CI on PR2 goes red intermittently. DoD #7 asks for green CI on the PRs.
- **Production scenario (the classification question in item 3):**
  - Validate gives the sample call exactly `timeout_ms`, and the first call on a fresh instance is the expensive one.
  - Live traffic and background preload (GOMAXPROCS parallel compiles) share the CPU with it.
  - Under load, a correct module comes back `ok:false, sample_call: timeout after 50 ms`. Per handoff line 560, T4 then marks it `rejected` for good.
  - The cause is CPU contention, a platform condition, reported as a module defect. The demo hook uses 50 ms.
  - Spec §7.3 does prescribe "deadline = `timeout_ms`", so this half is an owner decision, not a contract violation.
  - Related, by reading only (not reproduced): `extismrt.Compile` runs its trial instantiation under the same `timeout_ms` and wraps a failure as `ErrInvalidModule`.
    - On the hot path, `executor.compile` then stores the module in `e.invalid` for the life of the process.
    - So a contention spike during the background preload after every snapshot could, in principle, pin a good module as `handler_error` until restart.
    - This is M0 code. Check whether wazero honours the deadline during instantiation before acting on it.
- **Fix:**
  - Tests (must, before merge): use a generous `timeout_ms` (for example 2000) at the two sites that run a real sample call: `controlplane_test.go:245` and the shared hook in `validate_test.go:30`. Keep a short timeout only in the `infinite-loop` case, which is about timeouts. `internalapi_test.go` uses a fake validator, and the fake control plane's hooks never run a script, so neither needs a change.
  - Product (owner's call, cheap):
    - make one warm-up call before the timed sample call;
    - or retry a timed-out sample call once before reporting `timeout`;
    - or report `timeout` with a marker that T4 treats as retryable.
  - Write the choice into §7.3 and the T4 criteria.

#### I2. `DataPlaneClient.validate` lets dropped connections and malformed replies escape unmapped; the T4 handoff tells the backend developer to catch only the mapped errors
- **Location:**
  - `control-plane/src/controlplane/dpclient/client.py:40-48` (catches only `httpx.ConnectError` and `httpx.TimeoutException`);
  - `docs/handoff/backend.md:559-560` (T4: "`DataPlaneUnavailable` (503, no connection, timeout) → retry; `DataPlaneError` → retry");
  - `cli.py:162` (the same two-exception catch).
- **Evidence:** an experiment with raw-socket servers against the real client (`scratchpad/r2/dpclient_exp.py`):

  | Server behaviour | What escapes |
  |---|---|
  | Closes after reading the request | `httpx.RemoteProtocolError`, unmapped |
  | Sends half a body, then closes | `httpx.ReadError`, unmapped |
  | RST | `httpx.ReadError`, unmapped |
  | 200 with a check name outside the enum | `pydantic_core.ValidationError`, unmapped |

- **Failure scenario:**
  1. The data plane is redeployed or crashes while a validation is in flight. Or its 60 s `WriteTimeout` cuts a slow validation: the handler keeps running, but the write fails and the connection is closed.
  2. The connection drops, and `validate()` raises `RemoteProtocolError` or `ReadError`.
  3. A T4 worker written to the handoff catches `DataPlaneUnavailable` and `DataPlaneError` only. The background task dies with an uncaught exception.
  4. The module stays `validating` until the next control-plane restart re-queues it.
- **Today's impact:** `controlplane validate-module` and `seed-demo` print a traceback instead of "validation request failed".
- **Fix:**
  - Catch `httpx.TransportError`; it is the base of Connect, Read, Write, RemoteProtocol and Timeout errors. Map it to `DataPlaneUnavailable`.
  - Map a `ValidationError` on a 200 to `DataPlaneError`.
  - Add `MockTransport` tests that raise `RemoteProtocolError` and `ReadError`.
  - The handoff text can stay; it becomes true.

#### I3. The control plane accepts hook schemas the data plane cannot compile: the hook answers 503 forever, and Validate blames the module
- **Location:**
  - control plane: `control-plane/src/controlplane/hooks/rules.py:33-48`, which runs only `Draft202012Validator.check_schema` plus a remote-`$ref` filter;
  - data plane: `dataplane/internal/executor/validate.go:152-157` and `internalapi.go:82`. `HookDef.Validate` does not compile the schemas.
- **Root cause:** the two planes disagree on what a valid schema is.
  - Python `jsonschema` checks `pattern` with Python `re`; Go `santhosh-tekuri/jsonschema` uses RE2.
  - Python ignores `$schema` and does not resolve `$ref` during `check_schema`; the Go compiler loads `$schema` and resolves every `$ref` at compile time.
- **Evidence:** `POST /api/v1/hooks` against the compose. All three returned 201:
  - `pattern: "^(?=a)"` (a lookahead) in `input_schema` or `output_schema`;
  - `"$schema": "https://example.com/my-meta"`;
  - `"$ref": "#/$defs/nope"` inside `properties`, so the sample input never reaches it.
- **Hot path:** every invoke of a hook with such an input schema returns **503 `unavailable`** (`input schema of …: schema: compile schema: … not valid against metaschema` / `json-pointer … not found`). The snapshot still parses, so other hooks are unaffected; this is not a propagation stall.
  - `unavailable` tells SDKs to retry within the deadline (invoke contract), but the failure is permanent.
- **Validate:** for an `output_schema` the data plane cannot compile, the internal API returns **200** with `sample_call: "handler_error: output_schema does not compile: …"`. A correct module is then `rejected` (handoff line 560), with a report that blames the tenant's script for the operator's schema.
- **Fix:**
  - Data plane:
    - in `internalapi` (or at the top of `Validate`), compile `input_schema` and `output_schema` before `fetch`;
    - answer **400** on failure. Contract 400 already says "an invalid hook definition, retrying without changes is pointless";
    - drop the `handler_error: output_schema does not compile` branch.
  - Control plane, in `check_hook_spec`:
    - allow `$schema` only when absent or equal to the 2020-12 URI;
    - resolve every local `$ref` (walk the pointers);
    - reject regexes outside RE2 syntax. A cheap conservative check rejects `(?=`, `(?!`, `(?<=`, `(?<!` and backreferences `\1`–`\9`; the exact check is `google-re2` or asking the data plane.
  - Long term, the data plane can expose a "compile hook" check so there is one source of truth (MVP).
  - Add the rule to the hooks section of the handoff, because T1–T6 copy this slice.

---

## Minor

### Verified by experiment

**m1. A failed long-poll takes much longer than the 10 s backoff cap.**
- Location: `config/http_source.go:40-45`, which uses `http.DefaultTransport` (30 s dial timeout, no `ResponseHeaderTimeout`) under a 40 s `Client.Timeout`.
- Evidence: experiment C ran a 70 s outage in compose. There were only 7 attempts:
  - one dial to the stopped container's IP hung **30 s** (`dial tcp 172.21.0.5:8000: i/o timeout`);
  - every attempt after that spent about **8 s** on `lookup control-plane on 127.0.0.11:53: server misbehaving`.
- Scenario: the control plane comes back on a new IP while a dial to the old one is in flight. On Kubernetes this happens on every pod reschedule. In compose the IPs reshuffled once the data plane restarted during the outage. Pickup is then delayed by up to 30 s plus backoff.
- Effect on the demo: the e2e pickup limit (15 s, "backoff is at most 10 s") holds today only because compose gives `control-plane` the same IP on a plain stop/start.
- Fix: a dedicated `http.Transport` with `DialContext` timeout ≈ 3–5 s and `ResponseHeaderTimeout` = `wait_s + 10 s`.

**m2. Every failed poll is logged at ERROR with `retry_in` in nanoseconds.**
- Example: `"retry_in":6394121935`.
- slog's JSON handler prints a `time.Duration` as an int64, which is hard to read.
- Fix: log `wait.String()` or milliseconds.

**m3. The internal API reports a malformed hook definition as a module failure.**
- Location: `internalapi.go:78-85` together with `HookDef.Validate`.
- Evidence (all 200, not 400):
  - a request without `sample_input` → `sample_call: handler_error: … EOF while parsing`;
  - an empty `name` → accepted.
- The contract makes `sample_input` required. The control plane always sends it, so this is defence in depth.
- Fix: in `validateRequest`, require `sample_input` (and a non-empty `name`); the I3 schema compile belongs in the same place.

**m4. The hooks slice answers 500 instead of 422 for bad input; spec §6.6 requires 422 with the field.**
- Evidence (`POST /api/v1/hooks`):
  - `input_schema: {"$ref": "#/$defs/nope"}` → 500 (`referencing` raises `Unresolvable` while checking `sample_input`);
  - `"\u0000"` inside a schema string → 500 (PostgreSQL jsonb rejects NUL);
  - `"maximum": 1e400` → 500 (pydantic parses `inf`, which the JSON encoder cannot store).

  The last one is on the write path; it is not review-1 M8.
- This is the reference slice, so T1–T6 will copy the gap.
- Fix:
  - catch `referencing.exceptions.Unresolvable` → 422 (together with I3);
  - reject NUL and non-finite numbers in a validator on `HookSpec`.

**m5. The dev seed validates with an empty config but binds `DEFAULT_CONFIG`.**
- Location: `devtools/seed.py:221` (`dp.validate(content_hash, hook, None)`) and `devtools/validate_module.py:106/111`.
- The new `config` field exists for exactly this case, so the sample call never exercises the config the binding will run with.
- T4 and T5 will copy the seed.
- Fix: pass `config_dict` to `validate`.

**m6. `TestInternalValidate`'s report decode ignores errors and prints no detail.**
- Location: `cmd/dataplane/controlplane_test.go:256`.
- I had to instrument a copy to diagnose I1.
- Fix: print `report.Checks` in the failure message, as my scratch copy does.

### By reading

**m7. A server that answers 200 regardless of `after_version` makes the poller hot-loop.**
- Location: `config/poller.go:41-46`.
- `ErrStaleSnapshot` sets `err = nil`, which resets `failures` and refetches at once.
- Scenario: the real control plane never does this (review 1, Q3). An older control-plane build, a caching proxy that ignores the query string, or a hand-written fake does. The data plane then downloads the full snapshot in a tight loop.
- Fix: after a stale snapshot, wait `MinBackoff` before the next request (or count it as a failure).

**m8. Two preloads run at once at start-up.**
- Location: `cmd/dataplane/main.go:242-286`.
- The first `OnUpdate` puts a kick into `preloadKick`. The background loop starts at line 263, takes the kick, and runs `Preload` concurrently with the initial `Preload` at line 284.
- Singleflight removes the duplicate compiles, so the effect is harmless. It still contradicts spec §7.1 ("не больше одного прогона").
- Fix: drain `preloadKick` before starting the loop.

**m9. Demo step 1 never shows the 401 → 404 transition.**
- Location: `e2e/internal/scenario/scenario.go:186-222`.
- In both runs the step said "404 on the first call (key already there)". `docker compose exec` takes about 1.5 s, which is longer than propagation.
- The step proves the key arrived, not how fast. The EXPECTED column ("401 -> 404 within 3s") overstates it.
- Fix: reword EXPECTED to "401 → 404 (key reaches the data plane)", or time from the DB commit.

**m10. Demo step 3 checks a short window, and CI never runs a long outage.**
- Location: `scenario.go:257-295`.
- The 20 calls take about 100 ms right after the stop. They do come after at least one failed poll, because the long-poll is cut during the stop.
- Long-outage behaviour (a backoff ceiling of ≥ 8 s, DNS failures) was shown only by experiment C in this review.
- Acceptable for the DoD. Mention it in the walkthrough instead of claiming more.

**m11. The contract omits the internal API's 413.**
- Location: `api/dataplane-internal.openapi.yaml`, Validate responses.
- `internalapi.go:68` returns 413 for a body over 1 MiB (verified).
- Fix: add a `'413'` response.

**m12. `S3.Get` turns an object larger than 64 MiB into a transient error.**
- Location: `modstore/s3.go:153-155`.
- An oversize object yields a plain error, so Validate answers 503 forever and T4 retries forever, although the module is the problem. The hot path treats it as `errFetch` and refetches 64 MiB on every call.
- Unreachable through the control plane's 10 MiB upload cap. Bypass writes (the seed, `mc`) can reach it.
- Fix: a typed `ErrTooLarge` → `fetch` failed.

**m13. `run` does not watch `internalErr` while it waits for the first snapshot.**
- Location: `cmd/dataplane/main.go:257-262`.
- If the internal listener's `Serve` failed there, the process would not notice until shutdown.
- Practically unreachable once `Listen` has succeeded. Add the case for symmetry.

**m14. Docker can kill the data plane before a long shutdown finishes.**
- Location: `cmd/dataplane/main.go:301-310` and `compose.yaml` (`dataplane` has no `stop_grace_period`).
- Shutdown gives in-flight calls up to `MaxTimeoutMS + 5 s` (35 s), and `rt.Close` waits for running compiles, which ignore `ctx`.
- Docker's default grace period is 10 s. A `docker compose stop` that lands during a long compile or a 30 s hook therefore ends in SIGKILL, and the orderly close of the pools and runtime never runs.
- Harmless for the demo modules: the measured stop took 1.03 s.
- Fix: `stop_grace_period: 40s` on `dataplane`, or a note in the compose file.

**m15. CI and compose pull floating images.**
- `chainguard/minio:latest` and `chainguard/minio-client:latest` are already ruled on. `golang:1.26` and `distroless/static-debian12` are unpinned too, and the distroless image runs as root.
- An upstream image change can break the `e2e` job without a commit.
- Fix: pin by digest, and use `static-debian12:nonroot` for the data plane.

---

## Checked and found sound

- Every check below was verified by experiment unless it says "by reading".

- **Poller and HTTPSource:**
  - Wrong token: 401 → `ErrUnauthorized`, retries, a clear message, `readyz` 503, invoke 503 `unavailable`.
  - Rejected snapshot: backs off (unit test), and the Store keeps its last snapshot.
  - Shutdown while long-polling: under 100 ms in the unit test; `docker compose stop dataplane` took 1.03 s with exit 0.
  - Shutdown while waiting for the first snapshot (data plane restarted during the outage): 1.0 s, clean.
- **No leaks:** a flapping fake ran for 3 s and 5,028 requests (500 with a long body, 200, 304, dropped connection, rejected snapshot, 401). Goroutines were 3 before and 3 after.
- **Readiness (by reading and experiment):** 503 until the first snapshot plus preload. It then stays 200 through any outage and goes false only at shutdown. Compose deliberately health-checks `/healthz`.
- **Validate classification:**
  - Each storage failure maps to the right class (S1 row).
  - `ctx` cancellation during the slot wait, the instantiate or the sample call → 503 (by reading: `validate.go:76,129,140`).
  - `extismrt.classify` maps cancellation to `ErrTimeout`, so a caller who gave up during the sample call never becomes a module defect.
  - One exception: a cancellation during `Compile`'s trial instantiation comes back as a `compile` failure. It is harmless, because the caller has already gone and never reads that 200.
  - Module defects:
    - a missing export → `exports` failed;
    - a forbidden import → `imports` failed (`ModuleError` keeps the M0 message);
    - other `ErrInvalidModule` → `compile` failed;
    - a kernel instantiation failure → 503.
- **Module cache and pools untouched (by reading):** Validate calls `Runtime.Compile` directly, not `e.module`, and never touches the pool manager. `extismrt.New(Options{})` runs without a shared wazero `CompilationCache` (`runtime.go:82`), so closing a validated module frees its machine code. D23 holds for both caches.
  - The module is closed by `defer mod.Close` and the instance by `defer inst.Close`, both with `WithoutCancel`.
- **Semaphore:** acquired before `fetch` and released by `defer`; a waiter gives up with its request context (unit tests).
- **Internal API:**
  - constant-time token compare;
  - no listener without a token (unit test);
  - separate listener and port;
  - 1 MiB body cap → 413;
  - archtest rules: `gateway` ↛ `internalapi` ↛ `gateway`.
- **`modstore.S3`:**
  - Explicit region, so no bucket-location request.
  - `NoSuchKey` → `ErrNotFound`; every other client error is transient.
  - Hash checked after reading at most 64 MiB + 1.
  - The shared suite runs against both FS and the httptest fake.
- **Cross-check of the dpclient with the real Validate:**
  - The request body is `module_hash`, `hook` (from `HookDefOut.model_dump(mode="json")`) and an optional `config`; the Go decoder accepts it.
  - The response has `detail` omitted on passed checks on both sides.
  - 503 → `DataPlaneUnavailable`; 400 and 401 → `DataPlaneError`.
  - The e2e steps 6–7 exercise the whole path through MinIO.
- **Contract (by reading):** `dataplane-internal.openapi.yaml` (the `config` field, 400/401/503, Problem) matches the implementation except for m11.
- **e2e claims:**
  - Auth is checked before hook lookup (`httpapi.go`), so a 404 really proves the key arrived.
  - Every run uses fresh names.
  - Step 4's 400 can only come from the new schema, because the same payload got 200 before.
  - Step 5 compares the executed `module_hash` with the seeded one.
  - Weird hooks left in the snapshot by the I3 probes did not affect the second 7/7 run.
- **OpenAPI drift:** none.

---

## Triage of the deferred minors (checkpoint 1 and task notes)

| Item | Must fix before merge? | Reason |
|---|---|---|
| R1-M2: a disconnect does not end a long-poll handler | No | Bounded by `wait_s` ≤ 60. The data plane's own client gives up at 40 s, and no experiment showed growth. |
| R1-M4 rest: watcher liveness in `readyz`, rate-limited logs | No | The asyncpg `command_timeout` added in ada2f98 bounds the hang. A dead watcher still needs a `BaseException`. Keep it for MVP. |
| R1-M6: the cache is not dropped when the DB version goes backwards | No, but document it | It extends the §14 restore limitation: after a restore, restart the control plane as well as the data plane. Put one line in §14 or the handoff. |
| R1-M7: single-flight does not share failures | No | Now bounded by the 5 s connect and 15 s command timeouts. |
| R1-M8: jsonb numbers round-trip through float on read | No | It only loses precision; Parse accepts the result. Note that m4's `1e400` is a different, write-path problem. |
| R1-M10: `wait_s` validated without `after_version` | No | The data plane always sends both. A one-line contract wording fix whenever convenient. |
| R1-M11: DELETE of the `config_state` row | No | Only by hand; operational note. |
| R1-M15: auth lookups share the pool with the watcher | No | It only matters under an auth flood. The propagation bound held in every run (≤ 0.49 s). |
| T6: `BindingUpdate` accepts `{}` | No | Closed by the handoff ruling (T5 answers 400). |
| T6: `HookView` duplicates fields | No | Acceptable. |
| T7: the seed reads the hook and writes in separate transactions | No | Dev-only tool. The m5 fix (pass the config) is worth doing while there. |
| T7: the stored `validation_report` omits `detail` | No | Consistent with the ef2074d ruling and the frontend's zod schema. |
| T11: the S3 500 test takes about 3.7 s (minio retries) | No | Test time only. The same retries make Validate wait 6.2 s with MinIO down (experiment G), well inside the 30 s dpclient timeout. |
| T11: the oversize test allocates about 128 MB | No | Within CI limits. |

**Must fix before merge:** I1 (test deadlines; CI on PR2 is a DoD item), I2 and I3. None of the deferred minors.

---

## Experiments run (scripts in `scratchpad/r2`, outside the repos)

| # | What | Result |
|---|---|---|
| T | `go test -race -count=1 ./...` in `dataplane/` (worktree), then 3 runs in the copy, then targeted runs | 1 failure in the first run (`TestInternalValidate`); later full runs green |
| F | Instrumented `TestInternalValidate`, `-race -count=15` under a parallel `go build -race -a` | 2/15 fail: `sample_call: timeout after 50 ms` (I1) |
| CP | `pytest -q`, `ruff`, `ruff format --check`, `mypy`, `controlplane openapi` diff | 179 passed; clean; no drift |
| E | `docker compose up -d --build --wait`; `go run ./cmd/demo` twice | healthy in 13.6 s; 7/7 and 7/7 |
| M0 | `cd dataplane && go run ./cmd/demo` | 9/9 |
| A | Local data plane with a wrong `WASMHOOKS_INTERNAL_TOKEN` | `readyz` 503, invoke 503 `unavailable`, ERROR "rejected the internal token" on each retry |
| B (`expB.sh`) | Local data plane; `docker compose pause control-plane` 50 s; unpause; POST a hook | calls served; long-poll timed out at 40 s; pickup 0.43 s |
| C (`expC.sh`) | `stop control-plane` 70 s; restart the data plane during the outage; stop the data plane; start both | served, `readyz` 200; 7 attempts in 70 s (30 s dial hang, 8 s DNS failures, m1); restarted data plane `readyz` 503 (§14); stop 1.0 s; ready again in about 2 s |
| G (`expG.sh`) | Validate via a local data plane against the compose MinIO: good, NoSuchKey, timeouts of 1/2/5 ms, lookahead pattern, foreign `$schema`, missing `sample_input`, empty name, no token, 1 MiB body, public port, wrong bucket, wrong secret, MinIO stopped, tampered object, `http-call` | as in the S1 row, m3 and I3 |
| H | `POST /api/v1/hooks` with lookahead, foreign `$schema`, unresolvable `$ref` (nested and root), NUL, `1e400`; invoke the created hooks | 201 for the first three, then invoke 503 forever (I3); 500 for the rest (m4) |
| D (`dpclient_exp.py`) | `DataPlaneClient.validate` against raw sockets: close, half body, RST, bad enum | `RemoteProtocolError`, `ReadError`, `ReadError`, `ValidationError` escape (I2) |
| L | Poller against a flapping fake, 5,028 requests | goroutines 3 → 3; no leak |
| S | `docker compose stop dataplane` with a long-poll in flight | 1.03 s, exit 0 |
