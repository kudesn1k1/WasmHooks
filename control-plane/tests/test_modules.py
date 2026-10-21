import asyncio
import uuid
from collections.abc import AsyncIterator
from typing import Any

import httpx
import pytest
import pytest_asyncio
from asgi_lifespan import LifespanManager
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.app import create_app
from controlplane.auth.keys import create_api_key
from controlplane.auth.tenant_tokens import TenantTokens
from controlplane.configstate.schemas import HookDefOut
from controlplane.dpclient.schemas import ValidationReport
from controlplane.settings import Settings
from controlplane.storage.s3 import ModuleStorage, module_hash
from controlplane.tables import config_state, modules
from tests.factories import add_hook, add_tenant

HOOK = "checkout.discount"
URL = f"/api/v1/tenant/hooks/{HOOK}/modules"
WASM_A = b"\x00asm\x01\x00\x00\x00"
WASM_B = b"\x00asm\x01\x00\x00\x00second"


class DormantValidationClient:
    def __init__(self) -> None:
        self.called = asyncio.Event()

    async def validate(
        self, module_hash: str, hook: HookDefOut, config: dict[str, str] | None = None
    ) -> ValidationReport:
        self.called.set()
        await asyncio.Event().wait()
        raise AssertionError("unreachable")

    async def aclose(self) -> None:
        pass


@pytest.fixture
def module_settings(pg_url: str, engine: AsyncEngine, minio: dict[str, str]) -> Settings:
    return Settings(
        database_url=pg_url,
        internal_token="test-internal-token",
        tenant_jwt_secret="test-tenant-jwt-secret-0123456789abcdef",
        snapshot_poll_interval_s=0.05,
        s3_endpoint=minio["endpoint"],
        s3_bucket=minio["bucket"],
        s3_access_key=minio["access_key"],
        s3_secret_key=minio["secret_key"],
        s3_region=minio["region"],
        max_module_bytes=32,
    )


@pytest_asyncio.fixture
async def module_client(
    module_settings: Settings, validation_client: DormantValidationClient
) -> AsyncIterator[httpx.AsyncClient]:
    app = create_app(module_settings, validation_client=validation_client)
    async with LifespanManager(app):
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url="http://cp") as client:
            yield client


@pytest.fixture
def validation_client() -> DormantValidationClient:
    return DormantValidationClient()


def tenant_auth(settings: Settings, external_id: str) -> dict[str, str]:
    token = TenantTokens(
        settings.tenant_jwt_secret.get_secret_value(), settings.tenant_token_ttl_s
    ).issue(external_id)
    return {"Authorization": f"Bearer {token.token}"}


def wasm(data: bytes) -> dict[str, tuple[str, bytes, str]]:
    return {"file": ("module.wasm", data, "application/wasm")}


async def seed(engine: AsyncEngine, *external_ids: str) -> None:
    async with engine.begin() as conn:
        await add_hook(conn, HOOK)
        for external_id in external_ids:
            await add_tenant(conn, external_id)


async def snapshot_version(engine: AsyncEngine) -> int:
    async with engine.connect() as conn:
        return int((await conn.execute(select(config_state.c.version))).scalar_one())


async def test_upload_stores_module_and_metadata_without_snapshot_bump(
    module_client: httpx.AsyncClient,
    module_settings: Settings,
    engine: AsyncEngine,
    validation_client: DormantValidationClient,
) -> None:
    await seed(engine, "merchant-a")
    before = await snapshot_version(engine)
    resp = await module_client.post(
        URL,
        files=wasm(WASM_A),
        headers=tenant_auth(module_settings, "merchant-a"),
    )
    assert resp.status_code == 201, resp.text
    created = resp.json()
    assert created["hook"] == HOOK
    assert created["content_hash"] == module_hash(WASM_A)
    assert created["size_bytes"] == len(WASM_A)
    assert created["status"] == "uploaded"
    assert created["validation_report"] is None
    assert created["id"]
    assert created["uploaded_at"]
    assert await snapshot_version(engine) == before

    # The response does not wait for validation, but the committed id is queued.
    await asyncio.wait_for(validation_client.called.wait(), timeout=3)
    async with engine.connect() as conn:
        background_status = await conn.scalar(
            select(modules.c.status).where(modules.c.id == uuid.UUID(created["id"]))
        )
    assert background_status == "validating"

    storage = ModuleStorage.from_settings(module_settings)
    assert await storage.get_module(created["content_hash"]) == WASM_A
    async with engine.connect() as conn:
        assert (await conn.execute(select(func.count()).select_from(modules))).scalar_one() == 1


async def test_too_large_module_is_413(
    module_client: httpx.AsyncClient,
    module_settings: Settings,
    engine: AsyncEngine,
) -> None:
    await seed(engine, "merchant-a")
    resp = await module_client.post(
        URL,
        files=wasm(b"x" * (module_settings.max_module_bytes + 1)),
        headers=tenant_auth(module_settings, "merchant-a"),
    )
    assert resp.status_code == 413
    assert resp.json()["title"] == "Модуль слишком большой"


async def test_empty_module_is_422(
    module_client: httpx.AsyncClient,
    module_settings: Settings,
    engine: AsyncEngine,
) -> None:
    await seed(engine, "merchant-a")
    resp = await module_client.post(
        URL,
        files=wasm(b""),
        headers=tenant_auth(module_settings, "merchant-a"),
    )
    assert resp.status_code == 422


@pytest.mark.parametrize("method", ["GET", "POST"])
async def test_unknown_hook_is_404(
    module_client: httpx.AsyncClient,
    module_settings: Settings,
    engine: AsyncEngine,
    method: str,
) -> None:
    async with engine.begin() as conn:
        await add_tenant(conn, "merchant-a")
    kwargs: dict[str, Any] = {"files": wasm(WASM_A)} if method == "POST" else {}
    resp = await module_client.request(
        method,
        "/api/v1/tenant/hooks/unknown/modules",
        headers=tenant_auth(module_settings, "merchant-a"),
        **kwargs,
    )
    assert resp.status_code == 404
    assert resp.json()["title"] == "Хук не найден"


async def test_duplicate_for_same_tenant_and_hook_is_409(
    module_client: httpx.AsyncClient,
    module_settings: Settings,
    engine: AsyncEngine,
) -> None:
    await seed(engine, "merchant-a")
    auth = tenant_auth(module_settings, "merchant-a")
    assert (await module_client.post(URL, files=wasm(WASM_A), headers=auth)).status_code == 201
    resp = await module_client.post(URL, files=wasm(WASM_A), headers=auth)
    assert resp.status_code == 409
    assert resp.json()["title"] == "Такая версия уже загружена"
    assert resp.json()["detail"] == module_hash(WASM_A)


async def test_same_bytes_are_allowed_for_another_tenant(
    module_client: httpx.AsyncClient,
    module_settings: Settings,
    engine: AsyncEngine,
) -> None:
    await seed(engine, "merchant-a", "merchant-b")
    first = await module_client.post(
        URL, files=wasm(WASM_A), headers=tenant_auth(module_settings, "merchant-a")
    )
    second = await module_client.post(
        URL, files=wasm(WASM_A), headers=tenant_auth(module_settings, "merchant-b")
    )
    assert first.status_code == second.status_code == 201
    assert first.json()["content_hash"] == second.json()["content_hash"]
    assert first.json()["id"] != second.json()["id"]


async def test_list_is_newest_first_and_isolated_by_tenant(
    module_client: httpx.AsyncClient,
    module_settings: Settings,
    engine: AsyncEngine,
) -> None:
    await seed(engine, "merchant-a", "merchant-b")
    auth_a = tenant_auth(module_settings, "merchant-a")
    auth_b = tenant_auth(module_settings, "merchant-b")
    first = (await module_client.post(URL, files=wasm(WASM_A), headers=auth_a)).json()
    second = (await module_client.post(URL, files=wasm(WASM_B), headers=auth_a)).json()
    other = (await module_client.post(URL, files=wasm(WASM_A), headers=auth_b)).json()

    resp = await module_client.get(URL, headers=auth_a)
    assert resp.status_code == 200
    assert [item["id"] for item in resp.json()["items"]] == [second["id"], first["id"]]
    assert other["id"] not in {item["id"] for item in resp.json()["items"]}


@pytest.mark.parametrize("method", ["GET", "POST"])
async def test_requires_tenant_token(
    module_client: httpx.AsyncClient,
    module_settings: Settings,
    engine: AsyncEngine,
    method: str,
) -> None:
    await seed(engine, "merchant-a")
    async with engine.begin() as conn:
        operator = await create_api_key(conn, "operator")
    kwargs: dict[str, Any] = {"files": wasm(WASM_A)} if method == "POST" else {}
    assert (await module_client.request(method, URL, **kwargs)).status_code == 401
    resp = await module_client.request(
        method,
        URL,
        headers={"Authorization": f"Bearer {operator.token}"},
        **kwargs,
    )
    assert resp.status_code == 401
