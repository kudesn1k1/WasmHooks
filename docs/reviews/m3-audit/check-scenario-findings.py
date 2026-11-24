"""Offline evidence for the two kind scenario findings. Run with python -B."""
from bisect import bisect_left
from hashlib import sha256
import json
from pathlib import Path
import sys
from types import SimpleNamespace
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "deploy/kind"))
import scenario


def hash64(value):
    return int.from_bytes(sha256(value.encode()).digest()[:8], "big")


def ring_owner(addresses):
    points = sorted((hash64(f"{address}\0{n}"), address) for address in addresses for n in range(128))

    def owner(tenant):
        index = bisect_left(points, (hash64(tenant + "\0" + scenario.HOOK), ""))
        return points[index % len(points)][1]

    return owner


def check_replacement():
    archived = json.loads((ROOT / "docs/reviews/m3-kind-report.json").read_text(encoding="utf8"))
    for phase in ["baseline", "two_nodes", "recovered"]:
        owners = archived[phase]["owners"]
        owner = ring_owner(archived[phase]["pods"])
        assert all(owner(tenant) == actual for tenant, actual in owners.items())

    a, b, c, d = [f"10.244.0.{n}:9090" for n in (10, 11, 12, 16)]
    original = ring_owner([a, b, c])
    buckets = {addr: [] for addr in (a, b, c)}
    for index in range(1024):
        tenant = f"m3-000000028b-{index}"
        bucket = buckets[original(tenant)]
        if len(bucket) < 4:
            bucket.append(tenant)
        if all(len(bucket) == 4 for bucket in buckets.values()):
            break
    tenants = [*scenario.SHOPS, *(tenant for bucket in buckets.values() for tenant in bucket)]
    replacement = ring_owner([b, c, d])
    count = sum(replacement(tenant) == d for tenant in tenants)
    assert original("merchant-a") == a and len(tenants) == 14 and count == 0
    print(f"Healthy replacement owns {count} of {len(tenants)} sampled keys")


def check_status():
    platform = object.__new__(scenario.Platform)
    platform.args = SimpleNamespace(gateway_url="http://unused")
    platform.key = "test"
    platform.bindings = {"t": {}}
    for status in (200, 429):
        with patch.object(scenario, "request", return_value=(status, {"outcome": "unavailable"})):
            sample = platform.sample("t", "invoke")
        assert sample["outcome"] == "unavailable" and "error" not in sample
        load = object.__new__(scenario.Load)
        load.scheduled = {"loss_transition": 1}
        load.report = {"phases": {"loss_transition": {"load": {"requests": 1}, "samples": [sample]}}}
        load.check_results()
        print(f"Transition accepted HTTP {status} with unavailable")


if __name__ == "__main__":
    check_replacement()
    check_status()
