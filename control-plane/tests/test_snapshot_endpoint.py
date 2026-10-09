import asyncio
import time
from typing import Any

import httpx
import pytest
from sqlalchemy.ext.asyncio import AsyncConnection, AsyncEngine

from controlplane.configstate import snapshot as snapshot_module
from tests.factories import bump, seed_full, wasm_hash

URL = "/internal/v1/config/snapshot"
AUTH = {"Authorization": "Bearer test-internal-token"}


async def get(client: httpx.AsyncClient, **params: Any) -> httpx.Response:
    return await client.get(URL, params=params, headers=AUTH, timeout=70)


@pytest.mark.parametrize("headers", [{}, {"Authorization": "Bearer wrong"}])
async def test_requires_internal_token(client: httpx.AsyncClient, headers: dict[str, str]) -> None:
    resp = await client.get(URL, headers=headers)
    assert resp.status_code == 401
    assert resp.headers["content-type"].startswith("application/problem+json")


async def test_empty_installation(client: httpx.AsyncClient) -> None:
    resp = await get(client)
    assert resp.status_code == 200
    assert resp.json() == {"version": 1, "api_keys": [], "hooks": [], "tenants": [], "bindings": []}


async def test_older_client_gets_snapshot_at_once(client: httpx.AsyncClient) -> None:
    start = time.monotonic()
    resp = await get(client, after_version=0, wait_s=30)
    assert resp.status_code == 200
    assert resp.json()["version"] == 1
    assert time.monotonic() - start < 1


async def test_no_change_returns_304_after_wait(client: httpx.AsyncClient) -> None:
    start = time.monotonic()
    resp = await get(client, after_version=1, wait_s=1)
    elapsed = time.monotonic() - start
    assert resp.status_code == 304
    assert resp.content == b""
    assert 1 <= elapsed < 2


async def test_change_wakes_long_poll(client: httpx.AsyncClient, engine: AsyncEngine) -> None:
    request = asyncio.create_task(get(client, after_version=1, wait_s=5))
    await asyncio.sleep(0.2)
    changed_at = time.monotonic()
    await bump(engine)
    resp = await request
    assert resp.status_code == 200
    assert resp.json()["version"] == 2
    assert time.monotonic() - changed_at < 1


async def test_client_ahead_of_server_waits_and_gets_304(client: httpx.AsyncClient) -> None:
    # A data plane that remembers a newer version (e.g. the database was
    # restored from a backup) waits honestly instead of erroring.
    resp = await get(client, after_version=1000, wait_s=1)
    assert resp.status_code == 304


@pytest.mark.parametrize(
    "params",
    [
        {"after_version": "9223372036854775808"},
        {"after_version": "-1"},
        {"after_version": "abc"},
        {"after_version": "1", "wait_s": "0"},
        {"after_version": "1", "wait_s": "61"},
    ],
)
async def test_bad_parameters_are_400(client: httpx.AsyncClient, params: dict[str, str]) -> None:
    resp = await client.get(URL, params=params, headers=AUTH)
    assert resp.status_code == 400
    assert resp.headers["content-type"].startswith("application/problem+json")


async def test_snapshot_content_and_order(client: httpx.AsyncClient, engine: AsyncEngine) -> None:
    await seed_full(engine)
    snap = (await get(client)).json()

    assert snap["version"] == 2
    assert [k["id"] for k in snap["api_keys"]] == ["key_a", "key_b"]  # revoked one hidden
    assert [h["name"] for h in snap["hooks"]] == ["checkout.discount", "order.validate"]
    assert [t["external_id"] for t in snap["tenants"]] == ["merchant-a", "merchant-b"]
    assert snap["tenants"][0] == {
        "external_id": "merchant-a",
        "concurrency_limit": 4,
        "rate_limit_rps": 100,
    }
    # merchant-b has a binding without an active module: not in the snapshot.
    assert snap["bindings"] == [
        {
            "tenant_id": "merchant-a",
            "hook": "checkout.discount",
            "module_hash": wasm_hash("discount.wasm"),
            "config": {"threshold": "1000", "percent": "10"},
            "config_version": 1,
        }
    ]


async def test_concurrent_requests_build_once(
    client: httpx.AsyncClient, monkeypatch: pytest.MonkeyPatch
) -> None:
    calls = 0
    real = snapshot_module.read_snapshot

    async def counting(conn: AsyncConnection) -> snapshot_module.Snapshot:
        nonlocal calls
        calls += 1
        await asyncio.sleep(0.05)
        return await real(conn)

    monkeypatch.setattr(snapshot_module, "read_snapshot", counting)
    responses = await asyncio.gather(*(get(client) for _ in range(10)))
    assert all(r.status_code == 200 for r in responses)
    assert calls == 1
