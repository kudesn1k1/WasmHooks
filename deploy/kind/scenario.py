"""Run M3 against the dedicated wasmhooks-m3 kind cluster and write JSON evidence."""

import argparse
from collections import Counter, defaultdict
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import hashlib
import json
import math
import os
from pathlib import Path
import re
import subprocess
import sys
import threading
import time
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen
from uuid import uuid4


ROOT = Path(__file__).resolve().parents[2]
CLUSTER = "wasmhooks-m3"
CONTEXT = "kind-" + CLUSTER
NAMESPACE = "wasmhooks"
HOOK = "checkout.discount"
SHOPS = {"merchant-a": ("tea-sencha", 45000, 10), "merchant-b": ("coffee-ethiopia", 89000, 25)}
TRANSITIONS = {"loss_transition", "rejoin_transition"}
INVOKE_STATUS = {"ok": 200, "no_handler": 200, "handler_error": 200, "timeout": 200,
                 "quota_exceeded": 429, "unavailable": 503}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def utc():
    return datetime.now(timezone.utc).isoformat()


def request(method, url, token=None, body=None, content_type="application/json", statuses=(200,)):
    headers = {}
    if token:
        headers["Authorization"] = "Bearer " + token
    if body is not None:
        headers["Content-Type"] = content_type
        if not isinstance(body, bytes):
            body = json.dumps(body).encode()
    req = Request(url, data=body, headers=headers, method=method)
    try:
        resp = urlopen(req, timeout=5)
    except HTTPError as exc:
        resp = exc
    with resp:
        if resp.status not in statuses:
            raise ValueError(f"{method} {url}: HTTP {resp.status}, {resp.read(400).decode(errors='replace')}")
        return resp.status, json.load(resp)


def wait_for(description, seconds, observe):
    deadline = time.monotonic() + seconds
    while True:
        result = observe()
        if result and time.monotonic() <= deadline:
            return result
        if time.monotonic() >= deadline:
            raise TimeoutError(f"{description} did not converge within {seconds}s")
        time.sleep(min(0.15, max(0, deadline - time.monotonic())))


def kubectl(*args):
    result = subprocess.run(["kubectl", "--context", CONTEXT, "--namespace", NAMESPACE,
                             "--request-timeout=10s", *args], capture_output=True, text=True,
                            encoding="utf-8", timeout=18, check=False)
    if result.returncode:
        raise RuntimeError(f"kubectl {' '.join(args)}: {result.stderr.strip()}")
    return result.stdout


def schedulable_nodes(nodes):
    names = []
    for node in nodes:
        name = node["metadata"]["name"]
        require(re.fullmatch(r"wasmhooks-m3-(?:control-plane|worker)\d*", name)
                and node["spec"].get("providerID", "").startswith("kind://")
                and node["spec"]["providerID"].endswith(f"/{CLUSTER}/{name}"),
                f"refusing node mutation on unrecognized kind node {name}")
        if not node["spec"].get("unschedulable"):
            names.append(name)
    require(any(n["metadata"]["name"] == CLUSTER + "-control-plane" for n in nodes),
            "dedicated control-plane node missing")
    return names


def executor_pods():
    items = json.loads(kubectl("get", "pods", "-l", "app=executor", "-o", "json"))["items"]
    return {
        p["status"]["podIP"] + ":9090": {"name": p["metadata"]["name"], "uid": p["metadata"]["uid"],
                                         "node": p["spec"]["nodeName"]}
        for p in items if not p["metadata"].get("deletionTimestamp")
        and any(c["type"] == "Ready" and c["status"] == "True" for c in p["status"].get("conditions", []))
    }


def pod_logs(name):
    records = []
    for line in kubectl("logs", name).splitlines():
        if line.startswith("{"):
            records.append(json.loads(line))
    return records


def verify_execution_records(calls, logs):
    found = defaultdict(list)
    for pod, events in logs.items():
        for event in events:
            key = event.get("idempotency_key")
            if event.get("msg") == "invocation" and key in calls:
                require(event.get("executor_id") == pod and event.get("outcome") == "ok"
                        and event.get("hook") == HOOK, f"unexpected execution record for {key}: {event}")
                found[key].append((event.get("tenant_id"), pod, event.get("module_hash")))
    for key, expected in calls.items():
        require(found[key] == [expected], f"execution of {key}: {found[key]}, expected exactly {[expected]}")


def moved_pairs(before, after, dead):
    require(before.keys() == after.keys(), "ownership snapshot lost or gained pairs")
    moved = sorted(t for t in before if before[t] != after[t])
    require(moved and moved == sorted(t for t in before if before[t] == dead),
            f"ownership moved for {moved}, expected only all pairs of {dead}")
    return moved


def checkout_result(tenant, percent, body):
    _, subtotal, _ = SHOPS[tenant]
    discount = body["discount"]
    fallback = discount["fallback"]
    require(type(fallback) is bool, f"{tenant}: invalid fallback flag")
    expected = 0 if fallback else percent
    amount = subtotal * expected // 100
    require(body["shop_id"] == tenant and body["customer_id"] == "c-loyal"
            and body["subtotal"] == subtotal and body["total"] == subtotal - amount
            and discount["percent"] == expected and discount["amount"] == amount,
            f"{tenant}: incorrect checkout totals: {body}")
    outcome = discount["outcome"]
    if fallback:
        require(outcome in {"unavailable", "error", "timeout", "handler_error", "quota_exceeded",
                            "no_handler"} and discount["reason"] is None,
                f"{tenant}: malformed fallback: {discount}")
    else:
        require(outcome == "ok" and discount["reason"] == "loyal_customer", f"{tenant}: {discount}")
    return outcome, fallback


def summarize(samples):
    latencies = sorted(s["ms"] for s in samples)
    outcomes = Counter(s["outcome"] for s in samples)
    checkouts = sum(s["kind"] == "checkout" for s in samples)
    fallback = sum(s["fallback"] for s in samples)
    return {"requests": len(samples), "ok": outcomes["ok"], "unavailable": outcomes["unavailable"],
            "outcomes": dict(outcomes), "checkouts": checkouts, "checkout_fallbacks": fallback,
            "checkout_fallback_fraction": fallback / checkouts if checkouts else None,
            "p99_ms": latencies[math.ceil(len(latencies) * .99) - 1] if latencies else None}


def probe_module(wasm, run_id):
    # A WebAssembly custom section has a length-prefixed name followed by data.
    # Both lengths are below 128, so each unsigned LEB128 takes one byte.
    name = b"m3-probe"
    payload = bytes([len(name)]) + name + bytes.fromhex(run_id)
    return wasm + bytes([0, len(payload)]) + payload


class Platform:
    def __init__(self, args):
        self.args = args
        self.key = os.environ["SHOP_API_KEY"]
        self.internal_token = os.environ["WASMHOOKS_INTERNAL_TOKEN"]
        self.run_id = uuid4().hex[:10]
        self.bindings = {}

    def diagnostic(self, tenant):
        query = urlencode({"tenant": tenant, "hook": HOOK})
        return request("GET", f"{self.args.internal_url}/internal/v1/cluster?{query}",
                       self.internal_token)[1]

    def owner_map(self, tenants, members):
        owners = {}
        for tenant in tenants:
            state = self.diagnostic(tenant)
            require(set(state["members"]) == set(members) and state["owners"]
                    and state["owners"][0] in members, f"{tenant}: unstable ring: {state}")
            owners[tenant] = state["owners"][0]
        return owners

    def create_bindings(self, initial):
        spec = json.loads((ROOT / "examples/demo/snapshot.json").read_text(encoding="utf-8"))["hooks"][0]
        spec.pop("def_version")
        base = self.args.cp_url + "/api/v1"
        status, _ = request("POST", base + "/hooks", self.key, spec, statuses=(201, 409))
        if status == 409:
            # A repeated run resets the shared hook to this exact validated fixture.
            spec.pop("name")
            request("PUT", base + "/hooks/" + HOOK, self.key, spec)

        # Pick additional registered tenants evenly across the original real ring.
        # Include a unique probe initially owned by the affected T7 shop's executor.
        per_owner = {address: [] for address in sorted(initial)}
        for index in range(1024):
            tenant = f"m3-{self.run_id}-{index}"
            owner = self.owner_map([tenant], initial)[tenant]
            bucket = per_owner[owner]
            target = self.args.extra_pairs // 3 + (list(per_owner).index(owner) < self.args.extra_pairs % 3)
            if len(bucket) < target:
                bucket.append(tenant)
            if sum(map(len, per_owner.values())) == self.args.extra_pairs:
                break
        require(sum(map(len, per_owner.values())) == self.args.extra_pairs, "could not cover initial ring")
        victim = self.owner_map(["merchant-a"], initial)["merchant-a"]
        probe = per_owner[victim][0]
        wasm = (ROOT / "dataplane/testdata/wasm/discount.wasm").read_bytes()
        for index, tenant in enumerate([*SHOPS, *(t for bucket in per_owner.values() for t in bucket)]):
            percent = SHOPS[tenant][2] if tenant in SHOPS else 11 + index % 30
            binary = probe_module(wasm, self.run_id) if tenant == probe else wasm
            self.bindings[tenant] = self.register_binding(tenant, binary, percent)
        return victim, probe

    def register_binding(self, tenant, binary, percent):
        base = self.args.cp_url + "/api/v1"
        digest = "sha256:" + hashlib.sha256(binary).hexdigest()
        request("POST", base + "/tenants", self.key, {"external_id": tenant, "name": tenant,
                "rate_limit_rps": 100}, statuses=(201, 409))
        token = request("POST", f"{base}/tenants/{tenant}/console-sessions", self.key,
                        statuses=(201,))[1]["token"]
        hook = f"{base}/tenant/hooks/{HOOK}"
        boundary = "M3" + uuid4().hex
        part = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; '
                'filename="discount.wasm"\r\nContent-Type: application/wasm\r\n\r\n').encode()
        body = part + binary + f"\r\n--{boundary}--\r\n".encode()
        status, module = request("POST", hook + "/modules", token, body,
                                 f"multipart/form-data; boundary={boundary}", (201, 409))
        if status == 409:
            versions = request("GET", hook + "/modules", token)[1]["items"]
            module = next((m for m in versions if m["content_hash"] == digest), None)
            require(module, f"{tenant}: upload conflict without module {digest}")
        require(module["content_hash"] == digest, f"{tenant}: uploaded hash mismatch")

        def validated():
            versions = request("GET", hook + "/modules", token)[1]["items"]
            version = next((m for m in versions if m["id"] == module["id"]), None)
            if version and version["status"] in {"rejected", "archived"}:
                raise ValueError(f"{tenant} validation failed: {version['validation_report']}")
            return version and version["status"] == "validated"

        wait_for(f"{tenant} validation", 60, validated)
        request("PUT", hook + "/binding", token, {"active_module_id": module["id"],
                "config": {"percent": str(percent), "threshold": "1000"}})
        return {"module_hash": digest, "percent": percent}

    def verify_replacement_probe(self, pods, replacement):
        for index in range(1024):
            tenant = f"m3-{self.run_id}-rejoin-{index}"
            if self.owner_map([tenant], pods)[tenant] == replacement:
                break
        else:
            raise ValueError(f"could not find a pair owned by replacement {replacement}")

        wasm = (ROOT / "dataplane/testdata/wasm/discount.wasm").read_bytes()
        binding = self.register_binding(tenant, wasm, 11)
        pending = []

        def active_call():
            sample = self.sample(tenant, "invoke", binding)
            # The gateway and executors pick up the new binding independently.
            if sample["outcome"] in {"no_handler", "unavailable"}:
                pending.append(sample)
                return None
            require(sample["outcome"] == "ok", f"invalid replacement probe response: {sample}")
            return sample

        call = wait_for(f"{tenant} binding active", self.args.convergence_seconds, active_call)
        pod = pods[replacement]["name"]
        logs = {p["name"]: pod_logs(p["name"]) for p in pods.values()}
        verify_execution_records({call["key"]: (tenant, pod, binding["module_hash"])}, logs)
        return {"tenant": tenant, "pod": pod, "binding": binding, "binding_wait_samples": pending, "call": call}

    def sample(self, tenant, kind, binding=None):
        started = time.monotonic()
        sample = {"tenant": tenant, "kind": kind, "started_at": utc(), "fallback": False}
        if binding is None:
            binding = self.bindings[tenant]
        try:
            if kind == "checkout":
                body = {"customer_id": "c-loyal", "items": [{"product_id": SHOPS[tenant][0], "quantity": 1}]}
                _, checkout = request("POST", f"{self.args.shop_url}/api/v1/shops/{tenant}/checkout", body=body)
                sample["outcome"], sample["fallback"] = checkout_result(tenant, binding["percent"], checkout)
                sample["total"] = checkout["total"]
            else:
                sample["key"] = "m3-" + uuid4().hex
                body = {"tenant_id": tenant, "idempotency_key": sample["key"], "deadline_ms": 500,
                        "payload": {"cart_total": 450, "customer": {"id": "c-loyal", "lifetime_spend": 2400}}}
                status, resp = request("POST", f"{self.args.gateway_url}/v1/hooks/{HOOK}/invoke", self.key,
                                       body, statuses=(200, 429, 502, 503))
                sample["http_status"] = status
                if status == 502:
                    require(resp.get("status") == 502 and "outcome" not in resp, f"malformed 502 response: {resp}")
                    sample["outcome"] = "unknown_result"
                else:
                    sample["outcome"] = resp["outcome"]
                    require(INVOKE_STATUS.get(sample["outcome"]) == status, f"incorrect HTTP {status}: {resp}")
                    if sample["outcome"] == "ok":
                        require(resp["module_hash"] == binding["module_hash"]
                                and resp["result"]["discount_percent"] == binding["percent"]
                                and resp["result"]["reason"] == "loyal_customer", f"incorrect Invoke: {resp}")
        except Exception as exc:
            sample.update(error=str(exc), outcome="error")
        sample["ms"] = round((time.monotonic() - started) * 1000, 3)
        return sample


def counters(state):
    return {key: state[key] for key in ("attempts", "retries", "retry_successes", "attempts_by_executor")}


def counter_delta(start, end):
    result = {k: end[k] - start[k] for k in ("attempts", "retries", "retry_successes")}
    addresses = set(start["attempts_by_executor"]) | set(end["attempts_by_executor"])
    result["attempts_by_executor"] = {
        addr: end["attempts_by_executor"].get(addr, 0) - start["attempts_by_executor"].get(addr, 0)
        for addr in sorted(addresses)
    }
    return result


class Load:
    """Keep sending while the main thread checks the ring, logs and checkouts."""

    def __init__(self, platform, probe, report):
        self.platform = platform
        self.tenants = [t for t in platform.bindings if t != probe]
        self.stop = threading.Event()
        self.thread = threading.Thread(target=self.generate)
        self.lock = threading.Lock()
        self.phase = None
        self.report = report
        self.scheduled = Counter()

    def mark(self, phase):
        now = counters(self.platform.diagnostic("merchant-a"))
        with self.lock:
            if self.phase:
                record = self.report["phases"][self.phase]
                record.update(ended_at=utc(), gateway=counter_delta(record["counter_start"], now))
            self.phase = phase
            if phase:
                self.report["phases"][phase] = {"started_at": utc(), "counter_start": now, "samples": []}
        if phase:
            print(phase, flush=True)

    def record(self, phase, tenant, kind, source):
        result = self.platform.sample(tenant, kind)
        result["source"] = source
        with self.lock:
            self.report["phases"][phase]["samples"].append(result)
        return result

    def generate(self):
        target = time.monotonic()
        index = 0
        # Alternate a shop checkout and a synthetic pair so even short transition
        # windows exercise the customer path while the full ring remains loaded.
        shops = list(SHOPS)
        synthetic = [t for t in self.tenants if t not in SHOPS]
        with ThreadPoolExecutor(max_workers=max(8, self.platform.args.rps * 2)) as pool:
            while not self.stop.wait(max(0, target - time.monotonic())):
                shopping = index % 2 == 0
                group = shops if shopping else synthetic
                tenant = group[(index // 2) % len(group)]
                kind = "checkout" if shopping else "invoke"
                with self.lock:
                    phase = self.phase
                    self.scheduled[phase] += 1
                pool.submit(self.record, phase, tenant, kind, "load")
                index += 1
                target += 1 / self.platform.args.rps

    def verify_series(self, owners, pods):
        expected = {}
        series = self.report["phases"][self.phase]["verification"] = {"rounds": self.platform.args.series_rounds}
        for _ in range(self.platform.args.series_rounds):
            for tenant, owner in owners.items():
                binding = self.platform.bindings[tenant]
                if tenant in SHOPS:
                    checkout = self.record(self.phase, tenant, "checkout", "fixed_series")
                    require(checkout["outcome"] == "ok" and not checkout["fallback"],
                            f"{tenant}: checkout verification failed: {checkout}")
                call = self.record(self.phase, tenant, "invoke", "fixed_series")
                require(call["outcome"] == "ok", f"{tenant}: Invoke verification failed: {call}")
                expected[call["key"]] = (tenant, pods[owner]["name"], binding["module_hash"])
        logs = {p["name"]: pod_logs(p["name"]) for p in pods.values()}
        verify_execution_records(expected, logs)
        series["verified_invocations"] = len(expected)
        series["executions_by_pod"] = dict(Counter(pod for _, pod, _ in expected.values()))
        return logs

    def verify_recovered(self, previous, pods, replacement):
        owners = self.platform.owner_map(previous, pods)
        changed = sorted(t for t in previous if owners[t] != previous[t])
        require(all(owners[t] == replacement for t in changed), f"unexpected movement on rejoin: {changed}")
        recovered = {"pods": pods, "owners": owners, "moved_pairs": changed, "replacement": replacement}
        self.report["recovered"] = recovered
        self.verify_series(owners, pods)
        if replacement not in owners.values():
            recovered["replacement_probe"] = self.platform.verify_replacement_probe(pods, replacement)
        return recovered

    def finish(self):
        self.stop.set()
        self.thread.join()
        try:
            self.mark(None)
        finally:
            for phase, record in self.report["phases"].items():
                samples = record["samples"]
                record.update(summary=summarize(samples), load=summarize([s for s in samples if s["source"] == "load"]))

    def check_results(self):
        for phase, record in self.report["phases"].items():
            require(record["load"]["requests"] == self.scheduled[phase],
                    f"{phase}: missing results for scheduled traffic")
            require(record["load"]["requests"] > 0, f"{phase}: no scheduled load recorded")
            for sample in record["samples"]:
                if sample.get("error") or (phase not in TRANSITIONS and
                                           (sample["fallback"] or sample["outcome"] != "ok")):
                    raise ValueError(f"{phase}: invalid response: {sample}")
                if phase in TRANSITIONS and sample["kind"] == "invoke":
                    require(sample["outcome"] in {"ok", "unavailable", "unknown_result"},
                            f"{phase}: unexpected Invoke outcome: {sample}")


def matching_preloads(events, tenant, module_hash, pod):
    return [e for e in events if e.get("msg") == "module preloaded" and e.get("tenant_id") == tenant
            and e.get("hook") == HOOK and e.get("module_hash") == module_hash and e.get("executor_id") == pod]


def scenario(args, report):
    platform = Platform(args)
    nodes = json.loads(kubectl("get", "nodes", "-o", "json"))["items"]
    cordon_targets = schedulable_nodes(nodes)
    require(cordon_targets, "all nodes already cordoned")
    report["initial_node_scheduling"] = {n["metadata"]["name"]: not n["spec"].get("unschedulable", False)
                                         for n in nodes}
    initial = executor_pods()
    require(len(initial) == 3, f"expected three ready executor pods, found {initial}")
    state = wait_for("initial gateway ring", args.convergence_seconds,
                     lambda: ring_state(platform, lambda m: set(m) == set(initial)))
    config = {k: state[k] for k in ("discovery_interval_ms", "health_timeout_ms", "retry_min_budget_ms",
                                     "breaker_threshold", "breaker_cooldown_ms")}
    report["gateway_config"] = config
    victim, probe = platform.create_bindings(initial)
    before = platform.owner_map(platform.bindings, initial)
    require(set(before.values()) == set(initial), "test pairs do not cover all initial executors")
    report.update(bindings=platform.bindings, baseline={"pods": initial, "owners": before},
                  removed={"address": victim, **initial[victim]}, preload_probe={"tenant": probe})
    probe_hash = platform.bindings[probe]["module_hash"]
    victim_name = initial[victim]["name"]
    wait_for("initial owner preload", args.convergence_seconds,
             lambda: matching_preloads(pod_logs(victim_name), probe, probe_hash, victim_name))
    # Wait for the last CP snapshot to reach all processes before beginning the experiment.
    for tenant in platform.bindings:
        wait_for(f"{tenant} binding active", args.convergence_seconds,
                 lambda: platform.sample(tenant, "invoke")["outcome"] == "ok")

    cordoned = []

    def restore():
        problems = []
        for name in cordoned.copy():
            try:
                kubectl("uncordon", name)
                cordoned.remove(name)
            except Exception as exc:
                problems.append(str(exc))
        report["scheduling_restored"] = not cordoned
        if problems:
            raise RuntimeError(", ".join(problems))

    load = Load(platform, probe, report)
    load.mark("baseline")
    load.thread.start()
    try:
        time.sleep(args.phase_seconds)
        initial_logs = load.verify_series(before, initial)
        for addr, pod in initial.items():
            events = matching_preloads(initial_logs[pod["name"]], probe, probe_hash, pod["name"])
            require(bool(events) == (addr == victim), f"unexpected baseline probe preload on {addr}: {events}")
            if addr != victim:
                require(not any(e.get("module_hash") == probe_hash and e.get("msg") in {"module compiled", "invocation"}
                                for e in initial_logs[pod["name"]]), f"probe already used on {addr}")
        for name in cordon_targets:
            # Record intent first so an interrupted kubectl after a successful patch is recoverable.
            cordoned.append(name)
            kubectl("cordon", name)
        require(executor_pods() == initial, "executor set changed before failure injection")
        load.mark("loss_transition")
        loss_start = time.monotonic()
        report["deletion_requested_at"] = utc()
        kubectl("delete", "pod", victim_name, "--force", "--grace-period=0", "--wait=false")
        load.record("loss_transition", "merchant-a", "checkout", "affected_shop_after_delete")
        surviving = {addr: pod for addr, pod in initial.items() if addr != victim}
        wait_for("gateway removal", args.convergence_seconds - (time.monotonic() - loss_start),
                 lambda: ring_state(platform, lambda m: set(m) == set(surviving)))
        report["loss_convergence_ms"] = round((time.monotonic() - loss_start) * 1000, 3)
        load.mark("two_nodes")
        after = platform.owner_map(platform.bindings, surviving)
        moved = moved_pairs(before, after, victim)
        require(probe in moved and "merchant-a" in moved, "loss did not move probe and shop")
        require(executor_pods() == surviving, "replacement already ready during two-node verification")
        report["two_nodes"] = {"pods": surviving, "owners": after, "moved_pairs": moved}
        new_pod = surviving[after[probe]]["name"]
        preloads = wait_for("new owner probe preload before first caller", args.convergence_seconds,
                            lambda: matching_preloads(pod_logs(new_pod), probe, probe_hash, new_pod))
        # The probe is withheld from background load. This first call follows the
        # completed log read, so no timestamp synchronization between hosts is needed.
        proof = report["preload_probe"]
        history = [e for e in pod_logs(new_pod) if e.get("module_hash") == probe_hash]
        compiled = [e for e in history if e.get("msg") == "module compiled" and e.get("executor_id") == new_pod]
        require(len(compiled) == 1 and not any(e.get("msg") == "invocation" for e in history),
                f"expected a fresh probe compilation before first invocation on {new_pod}")
        proof.update(module_hash=probe_hash, original_pod=victim_name, new_pod=new_pod,
                     compile_event=compiled[0], preload_event=preloads[0], observed_before_first_call_at=utc())
        first = load.record("two_nodes", probe, "invoke", "first_after_move")
        require(first["outcome"] == "ok", f"first call after probe preload: {first}")
        proof["first_call"] = first
        verify_execution_records({first["key"]: (probe, new_pod, probe_hash)}, {new_pod: pod_logs(new_pod)})
        load.verify_series(after, surviving)
        time.sleep(args.phase_seconds)
        require(platform.owner_map(platform.bindings, surviving) == after, "two-node ownership changed")
        require(executor_pods() == surviving, "replacement entered during two-node series")

        load.mark("rejoin_transition")
        require(report["phases"]["two_nodes"]["gateway"]["attempts_by_executor"].get(victim, 0) == 0,
                "gateway attempted the dead address after convergence")
        rejoin_start = time.monotonic()
        restore()
        state = wait_for("gateway replacement", args.convergence_seconds - (time.monotonic() - rejoin_start),
                         lambda: ring_state(platform, lambda m: len(m) == 3 and set(surviving) < set(m)))
        report["rejoin_convergence_ms"] = round((time.monotonic() - rejoin_start) * 1000, 3)
        load.mark("recovered")
        final = executor_pods()
        require(set(final) == set(state["members"]), "recovered gateway membership differs from ready pods")
        replacement, = set(final) - set(surviving)
        require(final[replacement]["uid"] != initial[victim]["uid"], "replacement has original pod UID")
        recovered = load.verify_recovered(after, final, replacement)
        time.sleep(args.phase_seconds)
        require(platform.owner_map(after, final) == recovered["owners"], "recovered ownership changed")
    finally:
        try:
            restore()
        finally:
            load.finish()
    load.check_results()


def ring_state(platform, predicate):
    state = platform.diagnostic("merchant-a")
    return state if predicate(state["members"] or []) else None


def positive(value):
    number = int(value)
    if number <= 0:
        raise argparse.ArgumentTypeError("must be positive")
    return number


def source_identity():
    git = ["git", "-c", f"safe.directory={ROOT.as_posix()}"]
    paths = ["control-plane", "dataplane", "demo-shop", "deploy/kind"]
    files = subprocess.check_output([*git, "ls-files", "--cached", "--others", "--exclude-standard", "-z",
                                     "--", *paths], cwd=ROOT)
    digest = hashlib.sha256()
    for path in sorted(p for p in files.split(b"\0") if p):
        file = ROOT / os.fsdecode(path)
        if file.is_file():
            digest.update(path + b"\0" + hashlib.sha256(file.read_bytes()).digest())
    changed = subprocess.check_output([*git, "status", "--porcelain", "--", *paths], cwd=ROOT)
    return {"paths": paths, "dirty": bool(changed), "sha256": digest.hexdigest()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("cp", "gateway", "internal", "shop"):
        parser.add_argument(f"--{name}-url", required=True, type=lambda url: url.rstrip("/"))
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--revision")
    parser.add_argument("--rps", default=15, type=positive)
    parser.add_argument("--extra-pairs", default=12, type=positive)
    parser.add_argument("--phase-seconds", default=5, type=positive)
    parser.add_argument("--convergence-seconds", default=30, type=positive)
    parser.add_argument("--series-rounds", default=3, type=positive)
    args = parser.parse_args()
    if args.extra_pairs < 3:
        parser.error("--extra-pairs must cover at least three executor owners")
    report = {"result": "FAIL", "started_at": utc(), "phases": {}, "cluster": CONTEXT,
              "parameters": {k: v for k, v in vars(args).items() if k not in {"output", "revision"}}}
    try:
        report["revision"] = args.revision or subprocess.check_output(
            ["git", "-c", f"safe.directory={ROOT.as_posix()}", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
        report["working_tree"] = source_identity()
        scenario(args, report)
        report["result"] = "PASS"
    except (Exception, KeyboardInterrupt) as exc:
        report["error"] = f"{type(exc).__name__}: {exc}"
        print(report["error"], file=sys.stderr)
    finally:
        report["ended_at"] = utc()
        args.output.parent.mkdir(exist_ok=True, parents=True)
        args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"{report['result']} {args.output}")
    return int(report["result"] != "PASS")


if __name__ == "__main__":
    sys.exit(main())
