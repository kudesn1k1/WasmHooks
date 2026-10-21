from collections.abc import AsyncIterator
from typing import Any

import httpx
import pytest
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.auth.keys import create_api_key
from controlplane.auth.tenant_tokens import TenantTokens
from controlplane.settings import Settings
from controlplane.tables import config_state

URL = "/api/v1/tenants"
INTERNAL_AUTH = {"Authorization": "Bearer test-internal-token"}


@pytest.fixture
async def auth(engine: AsyncEngine) -> AsyncIterator[dict[str, str]]:
    async with engine.begin() as conn:
        key = await create_api_key(conn, "operator")
    yield {"Authorization": f"Bearer {key.token}"}


def tenant(external_id: str = "merchant-a", **overrides: Any) -> dict[str, Any]:
    return {"external_id": external_id, "name": "Merchant A"} | overrides


async def snapshot_version(engine: AsyncEngine) -> int:
    async with engine.connect() as conn:
        return int((await conn.execute(select(config_state.c.version))).scalar_one())


async def snapshot_after(client: httpx.AsyncClient, version: int) -> dict[str, Any]:
    resp = await client.get(
        "/internal/v1/config/snapshot",
        params={"after_version": version, "wait_s": 5},
        headers=INTERNAL_AUTH,
    )
    assert resp.status_code == 200, resp.text
    body: dict[str, Any] = resp.json()
    return body


async def test_create_tenant_uses_default_quotas_and_updates_snapshot(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    before = await snapshot_version(engine)
    resp = await client.post(URL, json=tenant(), headers=auth)
    assert resp.status_code == 201, resp.text
    created = resp.json()
    assert created["external_id"] == "merchant-a"
    assert created["name"] == "Merchant A"
    assert created["rate_limit_rps"] == 0
    assert created["concurrency_limit"] == 0
    assert await snapshot_version(engine) == before + 1

    snap = await snapshot_after(client, before)
    assert snap["tenants"] == [
        {"external_id": "merchant-a", "concurrency_limit": 0, "rate_limit_rps": 0}
    ]


async def test_duplicate_external_id_is_409_without_version_change(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    assert (await client.post(URL, json=tenant(), headers=auth)).status_code == 201
    before = await snapshot_version(engine)
    resp = await client.post(URL, json=tenant(name="Duplicate"), headers=auth)
    assert resp.status_code == 409
    assert resp.json()["title"] == "Тенант с таким external_id уже есть"
    assert resp.json()["detail"] == "merchant-a"
    assert await snapshot_version(engine) == before


async def test_list_is_sorted_and_get_returns_one(
    client: httpx.AsyncClient, auth: dict[str, str]
) -> None:
    second = tenant("merchant-b", name="Merchant B")
    first = tenant("merchant-a", name="Merchant A")
    assert (await client.post(URL, json=second, headers=auth)).status_code == 201
    created = (await client.post(URL, json=first, headers=auth)).json()

    listed = (await client.get(URL, headers=auth)).json()
    assert [item["external_id"] for item in listed["items"]] == ["merchant-a", "merchant-b"]
    assert (await client.get(f"{URL}/merchant-a", headers=auth)).json() == created


async def test_unknown_tenant_is_404(client: httpx.AsyncClient, auth: dict[str, str]) -> None:
    get_resp = await client.get(f"{URL}/unknown", headers=auth)
    patch_resp = await client.patch(f"{URL}/unknown", json={"name": "New"}, headers=auth)
    assert get_resp.status_code == patch_resp.status_code == 404
    assert get_resp.json()["title"] == patch_resp.json()["title"] == "Тенант не найден"


async def test_patch_quotas_updates_snapshot_once(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    created = (await client.post(URL, json=tenant(), headers=auth)).json()
    before = await snapshot_version(engine)
    resp = await client.patch(
        f"{URL}/merchant-a",
        json={"rate_limit_rps": 100, "concurrency_limit": 4},
        headers=auth,
    )
    assert resp.status_code == 200, resp.text
    updated = resp.json()
    assert updated["name"] == created["name"]
    assert updated["rate_limit_rps"] == 100
    assert updated["concurrency_limit"] == 4
    assert updated["updated_at"] > created["updated_at"]
    assert await snapshot_version(engine) == before + 1

    snap = await snapshot_after(client, before)
    assert snap["tenants"] == [
        {"external_id": "merchant-a", "concurrency_limit": 4, "rate_limit_rps": 100}
    ]


async def test_patch_name_does_not_update_snapshot_version(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    created = (await client.post(URL, json=tenant(), headers=auth)).json()
    before = await snapshot_version(engine)
    resp = await client.patch(f"{URL}/merchant-a", json={"name": "New name"}, headers=auth)
    assert resp.status_code == 200, resp.text
    assert resp.json()["name"] == "New name"
    assert resp.json()["updated_at"] > created["updated_at"]
    assert await snapshot_version(engine) == before


@pytest.mark.parametrize(
    "body",
    [{}, {"name": None}, {"name": "Merchant A"}, {"rate_limit_rps": 0, "concurrency_limit": 0}],
    ids=["empty", "null", "same-name", "same-quotas"],
)
async def test_patch_without_changes_keeps_version_and_timestamp(
    client: httpx.AsyncClient,
    engine: AsyncEngine,
    auth: dict[str, str],
    body: dict[str, Any],
) -> None:
    created = (await client.post(URL, json=tenant(), headers=auth)).json()
    before = await snapshot_version(engine)
    resp = await client.patch(f"{URL}/merchant-a", json=body, headers=auth)
    assert resp.status_code == 200, resp.text
    assert resp.json()["updated_at"] == created["updated_at"]
    assert await snapshot_version(engine) == before


@pytest.mark.parametrize(
    "body",
    [tenant(external_id=""), tenant(rate_limit_rps=-1), tenant(concurrency_limit=-1)],
    ids=["empty-external-id", "negative-rate", "negative-concurrency"],
)
async def test_invalid_create_is_422(
    client: httpx.AsyncClient, auth: dict[str, str], body: dict[str, Any]
) -> None:
    resp = await client.post(URL, json=body, headers=auth)
    assert resp.status_code == 422


@pytest.mark.parametrize(
    ("method", "path", "kwargs"),
    [
        ("GET", URL, {}),
        ("POST", URL, {"json": tenant()}),
        ("GET", f"{URL}/merchant-a", {}),
        ("PATCH", f"{URL}/merchant-a", {"json": {"name": "New"}}),
    ],
    ids=["list", "create", "get", "patch"],
)
async def test_requires_operator_key(
    client: httpx.AsyncClient,
    settings: Settings,
    method: str,
    path: str,
    kwargs: dict[str, Any],
) -> None:
    assert (await client.request(method, path, **kwargs)).status_code == 401
    tenant_token = TenantTokens(
        settings.tenant_jwt_secret.get_secret_value(), settings.tenant_token_ttl_s
    ).issue("merchant-a")
    resp = await client.request(
        method,
        path,
        headers={"Authorization": f"Bearer {tenant_token.token}"},
        **kwargs,
    )
    assert resp.status_code == 401


@pytest.mark.parametrize("field", ["rate_limit_rps", "concurrency_limit"])
async def test_patch_rejects_negative_quotas(
    client: httpx.AsyncClient, auth: dict[str, str], field: str
) -> None:
    assert (await client.post(URL, json=tenant(), headers=auth)).status_code == 201
    resp = await client.patch(f"{URL}/merchant-a", json={field: -1}, headers=auth)
    assert resp.status_code == 422


async def test_created_tenant_can_get_console_session(
    client: httpx.AsyncClient, auth: dict[str, str]
) -> None:
    assert (await client.post(URL, json=tenant(), headers=auth)).status_code == 201
    resp = await client.post(f"{URL}/merchant-a/console-sessions", headers=auth)
    assert resp.status_code == 201, resp.text
    assert resp.json()["token"]
    assert resp.json()["expires_at"]
