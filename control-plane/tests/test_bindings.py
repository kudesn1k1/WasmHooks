import asyncio
import uuid
from typing import Any

import httpx
import pytest
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.auth.keys import create_api_key
from controlplane.auth.tenant_tokens import TenantTokens
from controlplane.settings import Settings
from controlplane.tables import bindings, config_state
from tests.factories import add_hook, add_module, add_tenant

HOOK = "checkout.discount"
URL = f"/api/v1/tenant/hooks/{HOOK}/binding"
HASH_A = "sha256:" + "a" * 64
HASH_B = "sha256:" + "b" * 64


def tenant_auth(settings: Settings, external_id: str = "merchant-a") -> dict[str, str]:
    issued = TenantTokens(
        settings.tenant_jwt_secret.get_secret_value(), settings.tenant_token_ttl_s
    ).issue(external_id)
    return {"Authorization": f"Bearer {issued.token}"}


async def snapshot_version(engine: AsyncEngine) -> int:
    async with engine.connect() as conn:
        return int((await conn.execute(select(config_state.c.version))).scalar_one())


async def snapshot_after(client: httpx.AsyncClient, version: int) -> dict[str, Any]:
    resp = await client.get(
        "/internal/v1/config/snapshot",
        params={"after_version": version, "wait_s": 5},
        headers={"Authorization": "Bearer test-internal-token"},
    )
    assert resp.status_code == 200, resp.text
    body: dict[str, Any] = resp.json()
    return body


async def seed_modules(
    engine: AsyncEngine, *, status_a: str = "validated", status_b: str = "validated"
) -> tuple[uuid.UUID, uuid.UUID]:
    async with engine.begin() as conn:
        hook = await add_hook(conn, HOOK)
        tenant = await add_tenant(conn, "merchant-a")
        first = await add_module(conn, tenant, hook, HASH_A, status=status_a)
        second = await add_module(conn, tenant, hook, HASH_B, status=status_b)
    return first, second


@pytest.mark.parametrize("body", [{}, {"active_module_id": None}, {"config": None}])
async def test_empty_change_is_400(
    client: httpx.AsyncClient,
    settings: Settings,
    engine: AsyncEngine,
    body: dict[str, Any],
) -> None:
    await seed_modules(engine)
    resp = await client.put(URL, json=body, headers=tenant_auth(settings))
    assert resp.status_code == 400
    assert resp.json()["title"] == "Нечего менять"


async def test_extra_field_is_422(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    await seed_modules(engine)
    resp = await client.put(URL, json={"unexpected": True}, headers=tenant_auth(settings))
    assert resp.status_code == 422


async def test_unknown_hook_is_404(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        await add_tenant(conn, "merchant-a")
    resp = await client.put(
        "/api/v1/tenant/hooks/unknown/binding",
        json={"config": {}},
        headers=tenant_auth(settings),
    )
    assert resp.status_code == 404
    assert resp.json()["title"] == "Хук не найден"


async def test_invalid_or_unknown_module_id_is_404(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    await seed_modules(engine)
    for module_id in ["not-a-uuid", str(uuid.uuid4())]:
        resp = await client.put(
            URL,
            json={"active_module_id": module_id},
            headers=tenant_auth(settings),
        )
        assert resp.status_code == 404
        assert resp.json()["title"] == "Версия не найдена"


async def test_other_tenant_or_hook_module_is_404(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        hook = await add_hook(conn, HOOK)
        other_hook = await add_hook(conn, "order.validate")
        tenant = await add_tenant(conn, "merchant-a")
        other_tenant = await add_tenant(conn, "merchant-b")
        wrong_tenant = await add_module(conn, other_tenant, hook, HASH_A)
        wrong_hook = await add_module(conn, tenant, other_hook, HASH_B)
    for module_id in [wrong_tenant, wrong_hook]:
        resp = await client.put(
            URL,
            json={"active_module_id": str(module_id)},
            headers=tenant_auth(settings),
        )
        assert resp.status_code == 404
        assert resp.json()["title"] == "Версия не найдена"


@pytest.mark.parametrize("status", ["uploaded", "validating", "rejected", "archived"])
async def test_only_validated_module_can_be_activated(
    client: httpx.AsyncClient,
    settings: Settings,
    engine: AsyncEngine,
    status: str,
) -> None:
    module, _ = await seed_modules(engine, status_a=status)
    resp = await client.put(
        URL,
        json={"active_module_id": str(module)},
        headers=tenant_auth(settings),
    )
    assert resp.status_code == 409
    assert resp.json()["title"] == "Активировать можно только проверенную версию"
    assert resp.json()["detail"] == f"Статус: {status}"


async def test_activate_module_updates_snapshot_once(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    module, _ = await seed_modules(engine)
    before = await snapshot_version(engine)
    resp = await client.put(
        URL,
        json={"active_module_id": str(module)},
        headers=tenant_auth(settings),
    )
    assert resp.status_code == 200, resp.text
    binding = resp.json()
    assert binding["active_module_id"] == str(module)
    assert binding["active_module_hash"] == HASH_A
    assert binding["config"] == {}
    assert binding["config_version"] == 1
    assert await snapshot_version(engine) == before + 1

    snap = await snapshot_after(client, before)
    assert snap["bindings"] == [
        {
            "tenant_id": "merchant-a",
            "hook": HOOK,
            "module_hash": HASH_A,
            "config": {},
            "config_version": 1,
        }
    ]


async def test_config_without_active_module_is_kept_for_later_activation(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    module, _ = await seed_modules(engine)
    before = await snapshot_version(engine)
    configured = await client.put(
        URL,
        json={"config": {"percent": "10"}},
        headers=tenant_auth(settings),
    )
    assert configured.status_code == 200
    assert configured.json()["active_module_id"] is None
    assert configured.json()["active_module_hash"] is None
    assert configured.json()["config"] == {"percent": "10"}
    assert configured.json()["config_version"] == 1
    assert await snapshot_version(engine) == before + 1

    activated = await client.put(
        URL,
        json={"active_module_id": str(module)},
        headers=tenant_auth(settings),
    )
    assert activated.json()["config"] == {"percent": "10"}
    assert activated.json()["config_version"] == 1


async def test_config_change_increments_only_config_version(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    module_a, module_b = await seed_modules(engine)
    auth = tenant_auth(settings)
    first = (
        await client.put(
            URL,
            json={"active_module_id": str(module_a), "config": {"percent": "10"}},
            headers=auth,
        )
    ).json()
    assert first["config_version"] == 1

    switched = (
        await client.put(URL, json={"active_module_id": str(module_b)}, headers=auth)
    ).json()
    assert switched["config_version"] == 1

    changed = (await client.put(URL, json={"config": {"percent": "20"}}, headers=auth)).json()
    assert changed["config_version"] == 2


async def test_module_and_config_change_bump_snapshot_once(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    module, _ = await seed_modules(engine)
    before = await snapshot_version(engine)
    resp = await client.put(
        URL,
        json={"active_module_id": str(module), "config": {"percent": "10"}},
        headers=tenant_auth(settings),
    )
    assert resp.status_code == 200
    assert resp.json()["config_version"] == 1
    assert await snapshot_version(engine) == before + 1


async def test_noop_keeps_versions_and_timestamp(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    module, _ = await seed_modules(engine)
    auth = tenant_auth(settings)
    created = (
        await client.put(
            URL,
            json={"active_module_id": str(module), "config": {"percent": "10"}},
            headers=auth,
        )
    ).json()
    before = await snapshot_version(engine)
    same = (
        await client.put(
            URL,
            json={"active_module_id": str(module), "config": {"percent": "10"}},
            headers=auth,
        )
    ).json()
    assert same["updated_at"] == created["updated_at"]
    assert same["config_version"] == created["config_version"]
    assert await snapshot_version(engine) == before


async def test_rollback_restores_previous_module_in_snapshot(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    module_a, module_b = await seed_modules(engine)
    auth = tenant_auth(settings)
    await client.put(URL, json={"active_module_id": str(module_a)}, headers=auth)
    await client.put(URL, json={"active_module_id": str(module_b)}, headers=auth)
    before = await snapshot_version(engine)
    rolled_back = await client.put(URL, json={"active_module_id": str(module_a)}, headers=auth)
    assert rolled_back.status_code == 200
    assert rolled_back.json()["active_module_hash"] == HASH_A
    assert await snapshot_version(engine) == before + 1
    snap = await snapshot_after(client, before)
    assert snap["bindings"][0]["module_hash"] == HASH_A


async def test_concurrent_first_activation_is_safe_and_second_is_noop(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    module, _ = await seed_modules(engine)
    before = await snapshot_version(engine)
    auth = tenant_auth(settings)
    first, second = await asyncio.gather(
        client.put(URL, json={"active_module_id": str(module)}, headers=auth),
        client.put(URL, json={"active_module_id": str(module)}, headers=auth),
    )
    assert first.status_code == second.status_code == 200
    assert await snapshot_version(engine) == before + 1
    async with engine.connect() as conn:
        assert (await conn.execute(select(func.count()).select_from(bindings))).scalar_one() == 1


async def test_requires_tenant_token(
    client: httpx.AsyncClient, settings: Settings, engine: AsyncEngine
) -> None:
    module, _ = await seed_modules(engine)
    async with engine.begin() as conn:
        operator = await create_api_key(conn, "operator")
    body = {"active_module_id": str(module)}
    assert (await client.put(URL, json=body)).status_code == 401
    resp = await client.put(
        URL,
        json=body,
        headers={"Authorization": f"Bearer {operator.token}"},
    )
    assert resp.status_code == 401
