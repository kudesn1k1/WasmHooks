from pathlib import Path
from typing import Any

import pytest
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.configstate.schemas import HookDefOut
from controlplane.configstate.snapshot import read_snapshot
from controlplane.devtools.seed import seed_demo
from controlplane.devtools.validate_module import validate_module
from controlplane.dpclient.client import DataPlaneClient
from controlplane.dpclient.schemas import ValidationReport
from controlplane.settings import Settings
from controlplane.storage.s3 import ModuleStorage
from controlplane.tables import bindings, modules, tenants
from tests.factories import FIXTURES, add_hook, wasm_hash

WASM_BYTES = (FIXTURES / "discount.wasm").read_bytes()
OK = {"ok": True, "checks": [{"name": "fetch", "ok": True}]}
BAD = {"ok": False, "checks": [{"name": "imports", "ok": False, "detail": "nope"}]}


class FakeClient:
    def __init__(self, report: dict[str, Any]) -> None:
        self.report = report
        self.calls: list[tuple[str, HookDefOut, dict[str, str] | None]] = []
        self.closed = False

    async def validate(
        self, module_hash: str, hook: HookDefOut, config: dict[str, str] | None = None
    ) -> ValidationReport:
        self.calls.append((module_hash, hook, config))
        return ValidationReport.model_validate(self.report)

    async def aclose(self) -> None:
        self.closed = True


def as_client(fake: FakeClient) -> DataPlaneClient:
    return fake  # type: ignore[return-value]


@pytest.fixture
def storage(minio: dict[str, str]) -> ModuleStorage:
    return ModuleStorage(**minio)


@pytest.fixture
def wasm() -> Path:
    return FIXTURES / "discount.wasm"


async def count(engine: AsyncEngine, table: Any) -> int:
    async with engine.connect() as conn:
        return int(await conn.scalar(select(func.count()).select_from(table)) or 0)


async def snapshot_version(engine: AsyncEngine) -> int:
    async with engine.connect() as conn:
        return (await read_snapshot(conn)).version


async def test_seed_creates_tenant_module_binding(
    engine: AsyncEngine, settings: Settings, storage: ModuleStorage, wasm: Path
) -> None:
    async with engine.begin() as conn:
        await add_hook(conn, "checkout.discount")
    before = await snapshot_version(engine)
    fake = FakeClient(OK)
    hash_ = await seed_demo(settings, wasm, storage=storage, client=as_client(fake))

    assert hash_ == wasm_hash("discount.wasm")
    assert fake.calls[0][0] == hash_
    assert fake.calls[0][1].name == "checkout.discount"
    async with engine.connect() as conn:
        module = (await conn.execute(select(modules))).mappings().one()
        binding = (await conn.execute(select(bindings))).mappings().one()
        tenant = (await conn.execute(select(tenants))).mappings().one()
        snap = await read_snapshot(conn)
    assert tenant["external_id"] == "merchant-a"
    assert module["status"] == "validated"
    assert module["validation_report"] == OK
    assert module["size_bytes"] == len(WASM_BYTES)
    assert binding["active_module_id"] == module["id"]
    assert binding["config"] == {"threshold": "1000", "percent": "10"}
    assert snap.version == before + 1
    assert [(b.tenant_id, b.hook, b.module_hash) for b in snap.bindings] == [
        ("merchant-a", "checkout.discount", hash_)
    ]
    assert await storage.get_module(hash_) == WASM_BYTES


async def test_seed_rerun_is_idempotent(
    engine: AsyncEngine, settings: Settings, storage: ModuleStorage, wasm: Path
) -> None:
    async with engine.begin() as conn:
        await add_hook(conn, "checkout.discount")
    for _ in range(2):
        await seed_demo(settings, wasm, storage=storage, client=as_client(FakeClient(OK)))
    assert await count(engine, tenants) == 1
    assert await count(engine, modules) == 1
    assert await count(engine, bindings) == 1
    async with engine.connect() as conn:
        assert await conn.scalar(select(bindings.c.config_version)) == 1

    await seed_demo(
        settings, wasm, config={"percent": "20"}, storage=storage, client=as_client(FakeClient(OK))
    )
    async with engine.connect() as conn:
        row = (await conn.execute(select(bindings))).mappings().one()
    assert row["config"] == {"percent": "20"}
    assert row["config_version"] == 2


async def test_seed_rejected_module_changes_nothing(
    engine: AsyncEngine, settings: Settings, storage: ModuleStorage, wasm: Path
) -> None:
    async with engine.begin() as conn:
        await add_hook(conn, "checkout.discount")
    version_before = await snapshot_version(engine)
    with pytest.raises(RuntimeError, match="imports"):
        await seed_demo(settings, wasm, storage=storage, client=as_client(FakeClient(BAD)))
    assert await count(engine, tenants) == 0
    assert await count(engine, modules) == 0
    assert await count(engine, bindings) == 0
    assert await snapshot_version(engine) == version_before


async def test_validate_module_returns_report(
    engine: AsyncEngine, settings: Settings, storage: ModuleStorage, wasm: Path
) -> None:
    async with engine.begin() as conn:
        await add_hook(conn, "checkout.discount")
    fake = FakeClient(BAD)
    report = await validate_module(
        settings, wasm, "checkout.discount", storage=storage, client=as_client(fake)
    )
    assert report.ok is False
    assert fake.calls[0][0] == wasm_hash("discount.wasm")
    assert fake.calls[0][2] is None


async def test_validate_module_unknown_hook(
    engine: AsyncEngine, settings: Settings, storage: ModuleStorage, wasm: Path
) -> None:
    with pytest.raises(ValueError, match=r"no\.such"):
        await validate_module(
            settings, wasm, "no.such", storage=storage, client=as_client(FakeClient(OK))
        )
