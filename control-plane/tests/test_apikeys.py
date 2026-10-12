import asyncio
import re
from collections.abc import AsyncIterator
from typing import Any

import httpx
import pytest
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.auth.keys import create_api_key, hash_token
from controlplane.auth.tenant_tokens import TenantTokens
from controlplane.settings import Settings
from controlplane.tables import api_keys, config_state

URL = "/api/v1/api-keys"
INTERNAL_AUTH = {"Authorization": "Bearer test-internal-token"}


@pytest.fixture
async def auth(engine: AsyncEngine) -> AsyncIterator[dict[str, str]]:
    async with engine.begin() as conn:
        key = await create_api_key(conn, "operator")
    yield {"Authorization": f"Bearer {key.token}"}


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


async def test_create_key_stores_hash_updates_snapshot_and_authenticates(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    before = await snapshot_version(engine)
    resp = await client.post(URL, json={"name": "storefront"}, headers=auth)
    assert resp.status_code == 201, resp.text
    created = resp.json()
    assert re.fullmatch(r"key_[0-9a-f]{16}", created["id"])
    assert re.fullmatch(r"whk_[0-9A-Za-z]{32}", created["token"])
    assert created["name"] == "storefront"
    assert created["revoked_at"] is None
    assert await snapshot_version(engine) == before + 1

    async with engine.connect() as conn:
        stored = (
            await conn.execute(select(api_keys.c.sha256).where(api_keys.c.id == created["id"]))
        ).scalar_one()
    assert stored == hash_token(created["token"])

    snap = await snapshot_after(client, before)
    snapshot_key = next(key for key in snap["api_keys"] if key["id"] == created["id"])
    assert snapshot_key == {
        "id": created["id"],
        "name": "storefront",
        "sha256": hash_token(created["token"]),
    }
    new_auth = {"Authorization": f"Bearer {created['token']}"}
    assert (await client.get("/api/v1/hooks", headers=new_auth)).status_code == 200


async def test_duplicate_name_is_409_without_version_change(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    assert (await client.post(URL, json={"name": "ci"}, headers=auth)).status_code == 201
    before = await snapshot_version(engine)
    resp = await client.post(URL, json={"name": "ci"}, headers=auth)
    assert resp.status_code == 409
    assert resp.json()["title"] == "Ключ с таким именем уже есть"
    assert resp.json()["detail"] == "ci"
    assert await snapshot_version(engine) == before


async def test_concurrent_duplicate_creates_one_key(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    before = await snapshot_version(engine)
    first, second = await asyncio.gather(
        client.post(URL, json={"name": "race"}, headers=auth),
        client.post(URL, json={"name": "race"}, headers=auth),
    )
    assert sorted([first.status_code, second.status_code]) == [201, 409]
    assert await snapshot_version(engine) == before + 1


async def test_list_includes_revoked_keys_without_secrets(
    client: httpx.AsyncClient, auth: dict[str, str]
) -> None:
    created = (await client.post(URL, json={"name": "old"}, headers=auth)).json()
    assert (await client.delete(f"{URL}/{created['id']}", headers=auth)).status_code == 204

    resp = await client.get(URL, headers=auth)
    assert resp.status_code == 200
    old = next(item for item in resp.json()["items"] if item["id"] == created["id"])
    assert old["name"] == "old"
    assert old["revoked_at"] is not None
    assert "token" not in old
    assert "sha256" not in old


async def test_revoke_updates_snapshot_and_rejects_token(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    created = (await client.post(URL, json={"name": "temporary"}, headers=auth)).json()
    before = await snapshot_version(engine)
    resp = await client.delete(f"{URL}/{created['id']}", headers=auth)
    assert resp.status_code == 204
    assert not resp.content
    assert await snapshot_version(engine) == before + 1

    snap = await snapshot_after(client, before)
    assert created["id"] not in {key["id"] for key in snap["api_keys"]}
    revoked_auth = {"Authorization": f"Bearer {created['token']}"}
    assert (await client.get("/api/v1/hooks", headers=revoked_auth)).status_code == 401


async def test_repeated_revoke_is_idempotent(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    created = (await client.post(URL, json={"name": "temporary"}, headers=auth)).json()
    assert (await client.delete(f"{URL}/{created['id']}", headers=auth)).status_code == 204
    before = await snapshot_version(engine)
    assert (await client.delete(f"{URL}/{created['id']}", headers=auth)).status_code == 204
    assert await snapshot_version(engine) == before


async def test_unknown_key_is_404(client: httpx.AsyncClient, auth: dict[str, str]) -> None:
    resp = await client.delete(f"{URL}/key_unknown", headers=auth)
    assert resp.status_code == 404
    assert resp.json()["title"] == "Ключ не найден"


@pytest.mark.parametrize(
    ("method", "path", "kwargs"),
    [
        ("GET", URL, {}),
        ("POST", URL, {"json": {"name": "ci"}}),
        ("DELETE", f"{URL}/key_unknown", {}),
    ],
    ids=["list", "create", "revoke"],
)
async def test_requires_operator_key(
    client: httpx.AsyncClient,
    settings: Settings,
    method: str,
    path: str,
    kwargs: dict[str, Any],
) -> None:
    assert (await client.request(method, path, **kwargs)).status_code == 401
    token = TenantTokens(
        settings.tenant_jwt_secret.get_secret_value(), settings.tenant_token_ttl_s
    ).issue("merchant-a")
    resp = await client.request(
        method,
        path,
        headers={"Authorization": f"Bearer {token.token}"},
        **kwargs,
    )
    assert resp.status_code == 401
