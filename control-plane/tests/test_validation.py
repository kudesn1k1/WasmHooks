import asyncio
import uuid
from datetime import UTC, datetime, timedelta

import pytest
from asgi_lifespan import LifespanManager
from sqlalchemy import select, update
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.app import create_app
from controlplane.configstate.schemas import HookDefOut
from controlplane.dpclient.client import DataPlaneError, DataPlaneUnavailable
from controlplane.dpclient.schemas import ValidationCheck, ValidationReport
from controlplane.modules.validator import ModuleValidator
from controlplane.settings import Settings
from controlplane.tables import config_state, modules
from tests.factories import add_binding, add_hook, add_module, add_tenant

HASH_A = "sha256:" + "a" * 64
HASH_B = "sha256:" + "b" * 64
OK = ValidationReport(ok=True, checks=[ValidationCheck(name="fetch", ok=True)])
REJECTED = ValidationReport(
    ok=False,
    checks=[ValidationCheck(name="imports", ok=False, detail="http_request")],
)


class FakeClient:
    def __init__(
        self,
        engine: AsyncEngine,
        responses: list[ValidationReport | Exception],
    ) -> None:
        self._engine = engine
        self._responses = responses
        self.calls: list[tuple[str, HookDefOut, dict[str, str] | None]] = []
        self.statuses_during_calls: list[str] = []
        self.closed = False

    async def validate(
        self, module_hash: str, hook: HookDefOut, config: dict[str, str] | None = None
    ) -> ValidationReport:
        async with self._engine.connect() as conn:
            status = (
                await conn.execute(
                    select(modules.c.status).where(modules.c.content_hash == module_hash)
                )
            ).scalar_one()
        self.statuses_during_calls.append(str(status))
        self.calls.append((module_hash, hook, config))
        response = self._responses.pop(0)
        if isinstance(response, Exception):
            raise response
        return response

    async def aclose(self) -> None:
        self.closed = True


class BlockingClient:
    def __init__(self) -> None:
        self.called = asyncio.Event()
        self.closed = False

    async def validate(
        self, module_hash: str, hook: HookDefOut, config: dict[str, str] | None = None
    ) -> ValidationReport:
        self.called.set()
        await asyncio.Event().wait()
        raise AssertionError("unreachable")

    async def aclose(self) -> None:
        self.closed = True


async def module_state(engine: AsyncEngine, module_id: uuid.UUID) -> tuple[str, object]:
    async with engine.connect() as conn:
        row = (
            await conn.execute(
                select(modules.c.status, modules.c.validation_report).where(
                    modules.c.id == module_id
                )
            )
        ).one()
    return str(row.status), row.validation_report


async def snapshot_version(engine: AsyncEngine) -> int:
    async with engine.connect() as conn:
        return int((await conn.execute(select(config_state.c.version))).scalar_one())


async def wait_for_status(engine: AsyncEngine, module_id: uuid.UUID, expected: str) -> None:
    async with asyncio.timeout(3):
        while (await module_state(engine, module_id))[0] != expected:  # noqa: ASYNC110
            await asyncio.sleep(0.01)


async def seeded_module(
    engine: AsyncEngine,
    content_hash: str = HASH_A,
    *,
    status: str = "uploaded",
    config: dict[str, str] | None = None,
) -> uuid.UUID:
    async with engine.begin() as conn:
        hook = await add_hook(conn)
        tenant = await add_tenant(conn, "merchant-a")
        module = await add_module(conn, tenant, hook, content_hash, status=status)
        if config is not None:
            await add_binding(conn, tenant, hook, None, config)
    return module


@pytest.mark.parametrize(
    ("report", "expected_status"),
    [(OK, "validated"), (REJECTED, "rejected")],
    ids=["accepted", "rejected"],
)
async def test_validation_result_is_stored_without_snapshot_bump(
    engine: AsyncEngine, report: ValidationReport, expected_status: str
) -> None:
    module_id = await seeded_module(engine, config={"percent": "10"})
    before = await snapshot_version(engine)
    client = FakeClient(engine, [report])
    validator = ModuleValidator(engine, client, backoff_base_s=0)
    await validator.start()
    try:
        await wait_for_status(engine, module_id, expected_status)
    finally:
        await validator.stop()

    status, stored_report = await module_state(engine, module_id)
    assert status == expected_status
    assert stored_report == report.model_dump(mode="json", exclude_none=True)
    assert client.statuses_during_calls == ["validating"]
    assert client.calls[0][0] == HASH_A
    assert client.calls[0][1].name == "checkout.discount"
    assert client.calls[0][1].def_version == 1
    assert client.calls[0][2] == {"percent": "10"}
    assert await snapshot_version(engine) == before


async def test_missing_binding_omits_config(engine: AsyncEngine) -> None:
    module_id = await seeded_module(engine)
    client = FakeClient(engine, [OK])
    validator = ModuleValidator(engine, client, backoff_base_s=0)
    await validator.start()
    try:
        await wait_for_status(engine, module_id, "validated")
    finally:
        await validator.stop()
    assert client.calls[0][2] is None


async def test_unavailable_is_retried_while_status_stays_validating(
    engine: AsyncEngine,
) -> None:
    module_id = await seeded_module(engine)
    client = FakeClient(
        engine,
        [DataPlaneUnavailable("first"), DataPlaneUnavailable("second"), OK],
    )
    validator = ModuleValidator(engine, client, backoff_base_s=0)
    await validator.start()
    try:
        await wait_for_status(engine, module_id, "validated")
    finally:
        await validator.stop()
    assert len(client.calls) == 3
    assert client.statuses_during_calls == ["validating", "validating", "validating"]


async def test_data_plane_error_is_logged_and_retried(
    engine: AsyncEngine, caplog: pytest.LogCaptureFixture
) -> None:
    module_id = await seeded_module(engine)
    client = FakeClient(engine, [DataPlaneError("bad request"), OK])
    validator = ModuleValidator(engine, client, backoff_base_s=0)
    await validator.start()
    try:
        await wait_for_status(engine, module_id, "validated")
    finally:
        await validator.stop()
    assert len(client.calls) == 2
    assert "bad request" in caplog.text


async def test_startup_recovers_uploaded_and_validating_oldest_first(
    engine: AsyncEngine, settings: Settings
) -> None:
    async with engine.begin() as conn:
        hook = await add_hook(conn)
        tenant = await add_tenant(conn, "merchant-a")
        newer = await add_module(conn, tenant, hook, HASH_B, status="validating")
        older = await add_module(conn, tenant, hook, HASH_A, status="uploaded")
        now = datetime.now(UTC)
        await conn.execute(
            update(modules)
            .where(modules.c.id == older)
            .values(uploaded_at=now - timedelta(hours=1))
        )
        await conn.execute(update(modules).where(modules.c.id == newer).values(uploaded_at=now))

    client = FakeClient(engine, [OK, OK])
    app = create_app(settings, validation_client=client, validation_backoff_base_s=0)
    async with LifespanManager(app):
        await wait_for_status(engine, older, "validated")
        await wait_for_status(engine, newer, "validated")

    assert [call[0] for call in client.calls] == [HASH_A, HASH_B]
    assert client.closed


async def test_stop_cancels_inflight_validation_and_leaves_validating(
    engine: AsyncEngine,
) -> None:
    module_id = await seeded_module(engine)
    client = BlockingClient()
    validator = ModuleValidator(engine, client, backoff_base_s=0)
    await validator.start()
    await asyncio.wait_for(client.called.wait(), timeout=3)
    assert (await module_state(engine, module_id))[0] == "validating"
    await validator.stop()
    assert (await module_state(engine, module_id))[0] == "validating"
    assert client.closed
