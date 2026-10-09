# Review 1 — M1 control plane, Tasks 1–4

Scope: commits `dd08f62..e106b77` on `m1-control-plane` (diff package `review-1-package.diff`). All file:line references are to `e106b77`; the working tree already has unrelated in-progress edits (T5–T7) that were not reviewed. Tests, experiments and the Docker image were run on a `git archive e106b77` copy in a temp directory, never in the repo.

Baseline on that copy: `pytest` 95 passed, `mypy --strict` clean, `ruff check` and `ruff format --check` clean, `go test ./internal/config/` (golden) passes.

**Verdict:** 0 Critical, 3 Important, 15 Minor. The core claims hold. A snapshot of version v never misses a change ≤ v, and no DB-accepted state yields JSON that `config.Parse` rejects. However, one class of DB-accepted rows stops the snapshot being **built** at all, which stalls propagation exactly as a Parse rejection would. The D20 invariant also has no test that would catch a regression.

---

## Answers to the six questions

**Q1. Can snapshot v miss a change ≤ v? No (verified).**
- `bump_config_version` serialises writers on the `config_state` row lock (`configstate/version.py:21-26`). Postgres releases row locks only after the committing transaction leaves the ProcArray, so a snapshot that sees version v also sees every transaction with a version below v.
- The builder reads the version and all tables in one transaction that really is `REPEATABLE READ` and `READ ONLY` (`configstate/snapshot.py:76-78`). An experiment ran `SHOW transaction_isolation` inside `SnapshotService._engine.begin()` and got `repeatable read`, `read_only=on`. The base engine is unaffected (`read committed`).
- Interleaving experiment: 200 concurrent writers, half calling bump before their insert and half after, with random sleeps. Four readers built snapshots in a loop, with a sleep between the version read and the hooks read.
  - Under `REPEATABLE READ`: 0 of 33 snapshots missed a change and 0 contained a newer change.
  - Under `READ COMMITTED`: 0 missed, but 15 of 43 contained changes newer than their version. With bindings, that becomes a binding that references a hook absent from the same snapshot, which `Parse` rejects. See I2: no test would notice that regression.
- The cache (`snapshot.py:88-98`) only returns entries with `cached_version >= watcher.current()`. Builds are serialised, so the cache is monotonic. Watcher lag (≤ 1 poll interval) only delays delivery; it cannot lose a change: a client with `after_version=v` waits until the watcher passes v and then gets a build ≥ that version.
- The only way to serve stale content is the DB version going backwards (restore); see M6.

**Q2. Can the control plane emit a snapshot that Parse rejects? Not from DB-valid data (verified), but it can fail to emit any snapshot (I1).**
- Every `validate()` rule is backed by a constraint or a join:
  - `version ≥ 1`: CHECK.
  - Hook and tenant uniqueness: UNIQUE.
  - Binding uniqueness: PK on uuids, mapped through the unique `external_id`/`name`.
  - `def_version ≥ 1`, `timeout_ms` in [1, 30000], `memory_max_pages` in [16, 16384], `concurrency_limit ≥ 0`: CHECKs.
  - Schemas are objects: `jsonb_typeof` CHECKs.
  - `module_hash` and API-key `sha256` formats: regex CHECKs. Postgres `$` does not match before a trailing `\n`.
  - Bindings reference an existing hook and tenant: FKs plus inner joins.
- Types also line up: `uint32` for pages, `int64` for versions, `map[string]string` for config via `jsonb_string_values`.
- Experiment: jsonb values `1e400`, `-1e400`, `1e-400`, a 30-digit integer and a 36-digit decimal were pushed through the real builder. Every resulting body was accepted by Go `config.Parse`.
  - Postgres prints `1e400` as a plain integer and Python keeps it exact.
  - `1e-400` and long decimals are silently rounded; see M8.

**Q3. Long-poll under load and churn (verified under real uvicorn).**
- 100 concurrent waiters: one change woke all of them, and all 100 got 200 with the new version 0.59 s after the commit. Build-once was not measured here; it is covered by `test_concurrent_requests_build_once` and by reading the lock at `snapshot.py:91`. No busy loop: the current event is always a fresh, unset `Event`.
- Clients disconnecting mid-wait: 100 handlers stayed alive after their clients vanished. They were released on the next change, or would be at `wait_s`. This is bounded, not a leak (M2).
- DB stopped:
  - `/readyz` returned 503 problem+json.
  - Long-polls returned 304 at `wait_s`.
  - A request without `after_version` got 200 from the warm cache.
  - A cache miss got 500 `text/plain` (M1).
- DB restarted: the watcher recovered, and a new waiter got 200 0.67 s after the next commit.
- DB paused (hung): the watcher hung for the pause and recovered after unpause (0.21 s wake).
- Watcher task death: it cannot die from an `Exception`. If it did die, nothing would notice (M4).
- Shutdown with a long-poll in flight: uvicorn blocks and the container is SIGKILLed (M3).

**Q4. Authentication — no exploitable issue found.**
- Principal confusion is blocked in every direction (verified by the existing tests and extra experiments):
  - A tenant JWT on the operator endpoint gets 401.
  - An operator key on `TenantAuth` gets 401.
  - The internal token is only checked by `InternalAuth`, and only that router uses it.
  - An empty `CP_INTERNAL_TOKEN` leaves `/internal` closed (401 for `Bearer `, `Bearer` and no header), not open.
- Timing:
  - The internal token uses `hmac.compare_digest`, which leaks length only.
  - Operator keys are looked up by sha256 through an index, so the attacker cannot steer the lookup.
- JWT:
  - HS256 is pinned; HS512 with the same secret is rejected.
  - `exp`, `iat`, `sub`, `aud` and `iss` are required.
  - A future `iat` is rejected; a non-string `sub` is rejected.
- A holder of a tenant token can currently do nothing: no route mounts `TenantAuth` yet. Notes for T1/T6 are in M14.

**Q5. Contract — matches.**
- 200 is `application/json` with exactly `version, api_keys, hooks, tenants, bindings`, all required fields present and typed as in `dataplane-internal.openapi.yaml`. Empty collections are `[]`/`{}`, never null.
- 304 has no body and no content-length.
- Bad parameters get 400 problem+json.
- The contract test validates against the real OpenAPI document and has a guard against a permissive validator.
- One divergence: `wait_s` is validated even without `after_version` (M10).

**Q6. Other.**
- I3: `alembic_config` crashes on `%` in the DB URL.
- Test-hygiene items are collected in M13.

---

## Critical

None.

## Important

### Verified by experiment

#### I1. DB-accepted `text[]` values make the snapshot impossible to build, which stops propagation for every tenant
- **Location:** `tables.py:94-95`, `migrations/versions/0001_initial.py:102-103` (no element constraint); `configstate/schemas.py:22-23` (`list[str]`); `configstate/snapshot.py:65`.
- **What is wrong:** `allowed_host_functions` and `allowed_effect_types` are `text[] NOT NULL`. Postgres still accepts arrays that contain NULL elements (`ARRAY['discount', NULL]`) and multi-dimensional arrays (`'{{a},{b}}'`). `HookDefOut.model_validate` raises `ValidationError` on both. The build fails, the cache is never filled, and every request — including waiters woken by later, unrelated changes — gets 500.
- **This is a D22 gap.** It is not a Parse rejection, but it has the same "stall for good" effect that D22 exists to prevent. The `tables.py` docstring promises that "whatever the API or a hand-written query does" the DB refuses such rows. Hand-written writes are an explicit path (D21 "запись в БД в обход API"), and the in-progress `seed-demo` writes "bypassing the API".
- **Failure scenario (reproduced over HTTP against uvicorn):**
  1. `INSERT INTO hooks (..., allowed_effect_types) VALUES (..., ARRAY['discount', NULL])` and bump the version.
  2. The data plane's waiter gets 500.
  3. A later, unrelated tenant insert plus bump still gives 500, 500, 500.
  4. No configuration reaches the data plane until someone finds and fixes the row.
- **Fix:**
  - Add a CHECK to both columns, in the migration and in `tables.py`: `CASE WHEN coalesce(array_ndims(col), 1) <> 1 THEN false ELSE array_position(col, NULL) IS NULL END`. Use `CASE` because `array_position` raises on a multi-dimensional array, and `AND` does not guarantee evaluation order.
  - Add rejection tests to `test_tables.py`.
  - Optionally add a test that builds the snapshot from every row shape `test_tables` accepts and runs it through the golden path.

#### I2. The D20 invariant required by spec §9 has no test; dropping `REPEATABLE READ` passes the whole suite
- **Location:** `tests/test_config_version.py` (lock ordering only), `tests/test_snapshot_endpoint.py`; protected code `configstate/snapshot.py:76-78`.
- **What is wrong:** Spec §9 asks for "снимок версии v содержит все изменения с версией ≤ v (тест на ловушку D20)". The suite only proves that bumps serialise. Nothing checks that the builder reads one consistent snapshot.
- **Mutation evidence:**
  - Removing `isolation_level="REPEATABLE READ", postgresql_readonly=True` leaves 95 of 95 tests passing.
  - The interleaving experiment above shows `READ COMMITTED` produces snapshots labelled v that contain v+1 rows. With a binding that points at a hook created between the hooks query and the bindings query, `Parse` rejects that snapshot.
- **Failure scenario:**
  1. A later refactor of `SnapshotService`, or a "simplification" to `engine.begin()`, silently drops the isolation level.
  2. Under T5 activation traffic, a snapshot contains a binding whose hook is not in its `hooks` list.
  3. The data plane rejects it and backs off until the next version, and the suite stays green.
- **Fix:** add the spec's test.
  - Concurrent writers insert a hook and bump.
  - Concurrent readers call `SnapshotService` or `read_snapshot` under the service's engine.
  - For every snapshot v, assert hooks == {h : v_h ≤ v}. Make it two-sided, so it also catches "newer than v".
  - At minimum, also assert `SHOW transaction_isolation` == `repeatable read` inside the service's transaction.

#### I3. `alembic_config` crashes on any `%` in `CP_DATABASE_URL`; `controlplane migrate` fails
- **Location:** `cli.py:23` (`cfg.set_main_option("sqlalchemy.url", url)`); also `migrations/env.py:16` (`get_main_option`). Both go through ConfigParser interpolation.
- **What is wrong:** a URL-encoded password (`p%40ss`, `%2F`, `%25`, …) raises `ValueError: invalid interpolation syntax`. Experiment: `alembic_config("postgresql+asyncpg://u:p%40ss@…")` raised exactly that.
- **Failure scenario:**
  1. An operator sets `PG_PASSWORD` to a generated password containing `@`, `/` or `%`.
  2. They URL-encode it in the DSN, as required.
     - `compose.yaml:40` interpolates `${PG_PASSWORD}` into the DSN without encoding, so in compose such a password breaks the URL even before alembic sees it.
     - The `%` crash applies to every hand-written `CP_DATABASE_URL`: local uvicorn against compose PostgreSQL (handoff §3) and any managed PostgreSQL.
  3. The `control-plane-migrate` job exits non-zero, `control-plane` never starts, and `docker compose up --wait` fails.
  4. The pytest fixtures hide this, because the testcontainers password is `test`.
- **Fix:**
  - Either `cfg.set_main_option("sqlalchemy.url", url.replace("%", "%%"))`, or pass the URL through `cfg.attributes["url"]` and read it in `env.py` without interpolation.
  - Add a unit test with `%40` in the password.

---

## Minor

### Verified by experiment

**M1. Unhandled errors are `text/plain`, not problem+json.**
- Location: `errors.py:27-50` has no `Exception` handler.
- Scenario: with the DB down and the cache cold, the snapshot endpoint returns `500 text/plain "Internal Server Error"`. The I1 poison case does the same. This violates the global problem+json rule. The data plane only looks at the status, so it is harmless there; consoles will see non-JSON bodies.
- Fix: a generic handler that logs and returns `problem_response(500, "Внутренняя ошибка")`.

**M2. A disconnect does not end a long-poll handler.**
- Location: `configstate/router.py:24-26`, `configstate/watcher.py:57-70`.
- Scenario: Starlette does not cancel a plain-`Response` handler when its client disconnects. 100 abandoned waiters stayed registered until the next change, and would have stayed up to `wait_s` (≤ 60 s). This is bounded, not a leak. A data plane in a crash loop leaves up to `wait_s` × restart-rate idle coroutines.
- Note: the Review Focus 1 test (`tests/test_watcher.py:57-71`) exercises task cancellation, which is not the production path under uvicorn. The plan's claim ("ends no later than `wait_s`, nothing leaks") still holds.
- Fix: none needed. Optionally race `wait_newer` against `request.is_disconnected()` polling, and document the behaviour.

**M3. Graceful shutdown is blocked by in-flight long-polls.**
- Location: `Dockerfile:11` (uvicorn without `--timeout-graceful-shutdown`), `app.py:31-38`.
- Scenario: with one long-poll open, `docker stop` logged `Waiting for connections to close` and the container ended with exit 137 (SIGKILL). The lifespan cleanup (`watcher.stop`, `engine.dispose`) never ran. With Docker's usual 10 s grace period, every `docker compose stop control-plane` in demo step 3 takes ~10 s and kills the process. The data plane always holds a long-poll, so this always happens.
- Fix: `--timeout-graceful-shutdown 2–5`, and/or have shutdown wake waiters so they return 304 immediately.

**M5. Settings accept values that break the process.**
- Location: `settings.py:9-12`.
- Values accepted (verified): `snapshot_poll_interval_s=0` (or negative), `tenant_token_ttl_s=-5`, `internal_token=""`.
- Effects:
  - An interval ≤ 0 makes `_run` a hot loop of DB queries.
  - TTL ≤ 0 issues already-expired tokens.
  - An empty internal token makes `/internal` unreachable (closed, not open).
- Fix: `Field(gt=0)` on the interval and TTL, `min_length` (e.g. 16) on the internal token.

**M8. jsonb numbers round-trip through Python `float`.**
- Location: `configstate/snapshot.py:29-41,62-68` (jsonb decoded by `json.loads`, re-encoded by pydantic).
- Experiment: `1e-400` came out as `0.0`, and `3.14159265358979323846…` as `3.141592653589793`. A hook's `sample_input` or a schema bound such as `multipleOf` or `exclusiveMinimum` is silently altered in the snapshot. Parse still accepts it.
- Fix: select `col::text` and embed it raw (pydantic `Json[Any]` for input plus `RawJSON`, or orjson `Fragment`). This also removes a decode/encode pass per build.

**M9. Missing response headers.**
- Location: `errors.py:44-46`, `auth/deps.py:51-54`, `auth/tenant_tokens.py:63-64`.
- 401 responses carry no `WWW-Authenticate: Bearer` (RFC 9110 §11.6.1).
- The `StarletteHTTPException` handler drops `exc.headers`, so a 405 has no `Allow` header (verified on `POST /internal/v1/config/snapshot`).
- Fix: pass `headers` through `problem_response`, and add `WWW-Authenticate` on 401.

**M10. `wait_s` is validated even when the contract says it is ignored.**
- Location: `configstate/router.py:22`.
- `?wait_s=0` without `after_version` returns 400 (verified). The contract says `wait_s` is ignored when `after_version` is absent.
- Fix: harmless for the data plane, which always sends both. Either accept and ignore it, or reword the contract.

### By reading

**M4. Watcher has no timeouts, no liveness signal and noisy logs.**
- Location: `configstate/watcher.py:72-85`, `app.py:52-59`.
- `_read` has no timeout, and asyncpg has no `command_timeout`. A black-holed DB stalls the watcher until TCP gives up (about 15 min on Linux).
- If the task ever dies (only possible via `BaseException`), long-polls silently 304 forever while `/readyz` stays 200.
- Each failed poll logs a full traceback, 2/s with the default interval.
- Fix: `asyncio.timeout(...)` around `poll_once`, `connect_args={"timeout": 5, "command_timeout": 5}`, a "last successful poll" timestamp checked by `/readyz`, and rate-limited logging.

**M6. The snapshot cache is not invalidated when the DB version goes backwards (restore).**
- Location: `configstate/snapshot.py:89,93`.
- After a restore, `watcher.current()` drops below the cached version, and the `>=` check keeps serving the pre-restore bytes to fresh data planes. Post-restore changes stay invisible until the DB version passes the cached one.
- This extends the known §14 limitation to the control plane: a control-plane restart is needed as well.
- Fix: in `poll_once`, drop the cache when the version decreases (expose a callback or generation counter), or document it next to §14.

**M7. Single-flight does not share failures.**
- Location: `configstate/snapshot.py:91-98`.
- When a build raises, each request queued on the lock retries the build serially. With an unreachable DB and asyncpg's default 60 s connect timeout, N queued requests wait up to N × 60 s.
- Fix: share the in-flight result or exception with all waiters (a `Future`, as Go's `singleflight` does), plus the timeouts from M4.

**M11. Deleting the `config_state` row breaks every build and every write.**
- Location: `tables.py:38-45`.
- CHECK `id = 1` prevents a second row but not deletion. A `DELETE` or `TRUNCATE` makes every build and every `bump_config_version` raise `NoResultFound`.
- Fix: a trigger that forbids DELETE/TRUNCATE, or a note in the handoff.

**M12. No lock-order rule for `bump_config_version`.**
- Location: `configstate/version.py:10-20`.
- The docstring does not say where in the transaction to bump. A writer that bumps first holds the global lock for its whole transaction, which serialises all writers. It can also deadlock with a writer that locks a data row and then bumps.
- Fix: state "bump as the last statement before commit" in the docstring and in `docs/handoff/backend.md` (T1–T5 copy this pattern).

**M13. Test hygiene.**
- `tests/test_errors.py:83-95`: `test_fixture_isolation_b_sees_clean_db` passes trivially when run alone or in a different order (`CREATE TABLE IF NOT EXISTS` then count = 0). Fix: one test that writes, re-runs `reset_db`, and asserts.
- `tests/test_tables.py:239-248`: CHECK constraints in `tables.py` are never compared with the migration, so they can drift silently. Fix: compare `pg_get_constraintdef` for each `ck_*` against the compiled `CheckConstraint` text, or generate both from one constant.
- Golden data set (`tests/test_golden.py`): it has no non-empty `allowed_*` arrays and no binding with empty `config`. Those are the shapes most likely to diverge between the languages.
- `test_change_wakes_long_poll` only bumps the version. The plan asked for "+ вставка хука", so it never checks that the woken response contains the change.

**M14. Tenant-token notes for T1/T6.**
- Location: `auth/tenant_tokens.py`.
- Tokens bind to `external_id`, not the tenant uuid, and cannot be revoked. If T1's `PATCH /tenants/{external_id}` ever makes `external_id` mutable, a live token follows the string to whichever tenant takes that id next. Keep `external_id` immutable or put the uuid in the token.
- `verify` does not cap lifetime (`exp - iat ≤ ttl`). This is hardening only, since forging a token requires the secret.

**M15. `OperatorAuth` competes with the watcher for DB connections.**
- Location: `auth/deps.py:69-77`, `db.py:5`.
- Every operator request, authenticated or not, runs a DB query on the shared pool (5 + 10 connections, 30 s checkout timeout). An auth flood can make watcher polls wait for a connection, which pushes propagation past 1 s.
- Fix: give the watcher its own small pool or a dedicated connection, and add a cheap rate limit before the lookup.

---

## Checked and found sound (for the record)

- `bump_config_version`:
  - Writers serialise on the row lock (existing test).
  - 20 concurrent bumps hand out each version once.
  - Rollback leaves no trace.
- The router's 200/304 decisions are correct for:
  - a client ahead of the server;
  - `after_version` < current;
  - a watcher that lags behind a build;
  - the watcher at 0 because the DB was down at boot.

  There is no immediate-304 path that could hot-loop the data plane's "304 → retry at once".
- `wait_newer` leaves no stale `Event` waiters on timeout or cancellation.
- Woken waiters share one build: `test_concurrent_requests_build_once` (10 requests) and the lock at `snapshot.py:91`. This is from reading and the existing test; it was not measured in E5.
- Query parameters: `after_version=""`, `1e3`, `-1`, values above int64, `wait_s` 0 or 61 all give 400 problem+json.
- No blocking work on the event loop beyond one `model_dump_json` per version.
- The Go golden test runs in CI (`go test -race ./...` in `dataplane/`) and parses the committed golden file.

## Experiments run (scripts kept outside the repo)

| # | What | Result |
|---|---|---|
| E1 | `SHOW transaction_isolation` / `transaction_read_only` inside `SnapshotService._engine.begin()` | `repeatable read`, `on` |
| E2 | 200 interleaved writers × 4 slow readers, two-sided check; repeated under READ COMMITTED | RR: 0/33 bad; RC: 15/43 contain newer changes |
| E2b | Mutation: removed the isolation options, ran full suite | 95/95 pass (I2) |
| E3 | NULL-element and 2-D `text[]`; jsonb `1e400`, `-1e400`, `1e-400`, 30-digit int, long decimal → builder → Go `Parse` | arrays: `ValidationError` (I1); numbers: all parse, precision lost (M8) |
| E4 | `alembic_config` with `%40` in password | `ValueError: invalid interpolation syntax` (I3) |
| E5 | uvicorn: 100 waiters, clients killed, change; 100 live waiters, change | 100 handlers persist until change; all 100 → 200 in 0.59 s |
| E6 | uvicorn + `docker stop/start` and `pause/unpause` of Postgres | readyz 503; 304 at `wait_s`; cache-miss 500 `text/plain`; recovery 0.67 s / 0.21 s |
| E7 | Poison row over HTTP, then unrelated change | waiter 500; later polls 500, 500, 500 (I1) |
| E8 | Control-plane image, long-poll in flight, `docker stop` | "Waiting for connections to close", exit 137 (M3) |
| E9 | Settings/auth/param edge cases, JWT variants | as listed in M5, M9, M10, Q4 |
