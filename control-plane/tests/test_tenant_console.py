from typing import Any

import httpx
import pytest
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.auth.keys import create_api_key
from controlplane.auth.tenant_tokens import TenantTokens
from controlplane.settings import Settings
from controlplane.tables import config_state
from tests.factories import add_binding, add_hook, add_module, add_tenant

URL = "/api/v1/tenant/hooks"
HASH_A = "sha256:" + "a" * 64
HASH_B = "sha256:" + "b" * 64


def tenant_auth(settings: Settings, external_id: str) -> dict[str, str]:
    issued = TenantTokens(
        settings.tenant_jwt_secret.get_secret_value(), settings.tenant_token_ttl_s
    ).issue(external_id)
    return {"Authorization": f"Bearer {issued.token}"}


async def snapshot_version(engine: AsyncEngine) -> int:
    async with engine.connect() as conn:
        return int((await conn.execute(select(config_state.c.version))).scalar_one())


async def test_list_returns_all_hooks_sorted_with_no_bindings(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        await add_tenant(conn, "merchant-a")
        await add_hook(conn, "order.validate")
        await add_hook(conn, "checkout.discount")

    before = await snapshot_version(engine)
    resp = await client.get(URL, headers=tenant_auth(settings, "merchant-a"))
    assert resp.status_code == 200, resp.text
    items = resp.json()["items"]
    assert [item["hook"]["name"] for item in items] == ["checkout.discount", "order.validate"]
    assert [item["binding"] for item in items] == [None, None]
    assert "created_at" not in items[0]["hook"]
    assert "updated_at" not in items[0]["hook"]
    assert await snapshot_version(engine) == before


async def test_active_binding_contains_module_hash_and_config(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        hook = await add_hook(conn)
        tenant = await add_tenant(conn, "merchant-a")
        module = await add_module(conn, tenant, hook, HASH_A)
        await add_binding(conn, tenant, hook, module, {"threshold": "1000"})

    resp = await client.get(URL, headers=tenant_auth(settings, "merchant-a"))
    assert resp.status_code == 200, resp.text
    binding = resp.json()["items"][0]["binding"]
    assert binding["active_module_id"] == str(module)
    assert binding["active_module_hash"] == HASH_A
    assert binding["config"] == {"threshold": "1000"}
    assert binding["config_version"] == 1
    assert binding["updated_at"]


async def test_binding_without_active_module_has_null_module_fields(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        hook = await add_hook(conn)
        tenant = await add_tenant(conn, "merchant-a")
        await add_binding(conn, tenant, hook, None, {"threshold": "1000"})

    resp = await client.get(URL, headers=tenant_auth(settings, "merchant-a"))
    binding = resp.json()["items"][0]["binding"]
    assert binding["active_module_id"] is None
    assert binding["active_module_hash"] is None
    assert binding["config"] == {"threshold": "1000"}


async def test_other_tenant_binding_is_not_visible(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        hook = await add_hook(conn)
        await add_tenant(conn, "merchant-a")
        other = await add_tenant(conn, "merchant-b")
        module = await add_module(conn, other, hook, HASH_B)
        await add_binding(conn, other, hook, module, {"secret": "other"})

    resp = await client.get(URL, headers=tenant_auth(settings, "merchant-a"))
    assert resp.status_code == 200
    assert resp.json()["items"][0]["binding"] is None


async def test_get_returns_one_hook_and_own_binding(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        hook = await add_hook(conn)
        tenant = await add_tenant(conn, "merchant-a")
        module = await add_module(conn, tenant, hook, HASH_A)
        await add_binding(conn, tenant, hook, module)

    resp = await client.get(f"{URL}/checkout.discount", headers=tenant_auth(settings, "merchant-a"))
    assert resp.status_code == 200, resp.text
    assert resp.json()["hook"]["name"] == "checkout.discount"
    assert resp.json()["binding"]["active_module_id"] == str(module)


async def test_unknown_hook_is_404(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        await add_tenant(conn, "merchant-a")
    resp = await client.get(f"{URL}/unknown", headers=tenant_auth(settings, "merchant-a"))
    assert resp.status_code == 404
    assert resp.json()["title"] == "Хук не найден"


@pytest.mark.parametrize("path", [URL, f"{URL}/checkout.discount"], ids=["list", "get"])
async def test_requires_tenant_token(
    client: httpx.AsyncClient,
    settings: Settings,
    engine: AsyncEngine,
    path: str,
) -> None:
    async with engine.begin() as conn:
        operator = await create_api_key(conn, "operator")
    assert (await client.get(path)).status_code == 401
    resp = await client.get(path, headers={"Authorization": f"Bearer {operator.token}"})
    assert resp.status_code == 401


async def test_response_matches_frontend_shape(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        await add_tenant(conn, "merchant-a")
        await add_hook(conn)
    resp = await client.get(URL, headers=tenant_auth(settings, "merchant-a"))
    item: dict[str, Any] = resp.json()["items"][0]
    assert set(item) == {"hook", "binding"}
    assert set(item["hook"]) == {
        "name",
        "def_version",
        "input_schema",
        "output_schema",
        "timeout_ms",
        "memory_max_pages",
        "allowed_host_functions",
        "allowed_effect_types",
        "sample_input",
    }
