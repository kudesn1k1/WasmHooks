"""DEV-ONLY: seeds a demo tenant, module and binding, bypassing the upload flow."""

import uuid
from collections.abc import Mapping
from pathlib import Path

import anyio
from sqlalchemy import insert, select, update
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.configstate.version import bump_config_version
from controlplane.db import create_engine
from controlplane.devtools.hook_lookup import load_hook
from controlplane.dpclient.client import DataPlaneClient
from controlplane.dpclient.schemas import ValidationReport
from controlplane.settings import Settings
from controlplane.storage.s3 import ModuleStorage
from controlplane.tables import bindings, modules, tenants

DEFAULT_CONFIG = {"threshold": "1000", "percent": "10"}


async def _upsert_tenant(conn: AsyncConnection, external_id: str) -> uuid.UUID:
    await conn.execute(
        pg_insert(tenants)
        .values(external_id=external_id, name=external_id)
        .on_conflict_do_nothing(index_elements=[tenants.c.external_id])
    )
    result = await conn.execute(select(tenants.c.id).where(tenants.c.external_id == external_id))
    return uuid.UUID(str(result.scalar_one()))


async def _upsert_module(
    conn: AsyncConnection,
    tenant_id: uuid.UUID,
    hook_id: uuid.UUID,
    content_hash: str,
    size_bytes: int,
    report: ValidationReport,
) -> uuid.UUID:
    insert_stmt = pg_insert(modules).values(
        tenant_id=tenant_id,
        hook_id=hook_id,
        content_hash=content_hash,
        size_bytes=size_bytes,
        status="validated",
        validation_report=report.model_dump(mode="json", exclude_none=True),
    )
    stmt = insert_stmt.on_conflict_do_update(
        index_elements=[modules.c.tenant_id, modules.c.hook_id, modules.c.content_hash],
        set_={"status": "validated", "validation_report": insert_stmt.excluded.validation_report},
    ).returning(modules.c.id)
    return uuid.UUID(str((await conn.execute(stmt)).scalar_one()))


async def _upsert_binding(
    conn: AsyncConnection,
    tenant_id: uuid.UUID,
    hook_id: uuid.UUID,
    module_id: uuid.UUID,
    config: dict[str, str],
) -> None:
    where = (bindings.c.tenant_id == tenant_id) & (bindings.c.hook_id == hook_id)
    current = (
        await conn.execute(select(bindings.c.config, bindings.c.config_version).where(where))
    ).first()
    if current is None:
        await conn.execute(
            insert(bindings).values(
                tenant_id=tenant_id, hook_id=hook_id, active_module_id=module_id, config=config
            )
        )
        return
    values: dict[str, object] = {"active_module_id": module_id}
    if current.config != config:
        values["config"] = config
        values["config_version"] = current.config_version + 1
    await conn.execute(update(bindings).where(where).values(**values))


async def seed_demo(
    settings: Settings,
    wasm_path: Path,
    tenant: str = "merchant-a",
    hook_name: str = "checkout.discount",
    config: Mapping[str, str] | None = None,
    *,
    storage: ModuleStorage | None = None,
    client: DataPlaneClient | None = None,
) -> str:
    """DEV-ONLY. Returns the module hash. Raises ValueError if the hook does not
    exist and RuntimeError (with the report) if the data plane rejects the module.
    Rerunning changes no rows except the snapshot version."""
    config_dict = dict(DEFAULT_CONFIG if config is None else config)
    data = await anyio.Path(wasm_path).read_bytes()
    storage = storage or ModuleStorage.from_settings(settings)
    content_hash = await storage.put_module(data)

    engine = create_engine(settings.database_url)
    own_client = client is None
    dp = client or DataPlaneClient(
        settings.dataplane_internal_url, settings.internal_token.get_secret_value()
    )
    try:
        async with engine.connect() as conn:
            hook_id, hook = await load_hook(conn, hook_name)
        # The sample call runs with the config the binding will get.
        report = await dp.validate(content_hash, hook, config_dict)
        if not report.ok:
            raise RuntimeError(f"module failed validation: {report.model_dump_json()}")
        async with engine.begin() as conn:
            tenant_id = await _upsert_tenant(conn, tenant)
            module_id = await _upsert_module(
                conn, tenant_id, hook_id, content_hash, len(data), report
            )
            await _upsert_binding(conn, tenant_id, hook_id, module_id, config_dict)
            await bump_config_version(
                conn,
                "dev.seed",
                {"tenant": tenant, "hook": hook_name, "module_hash": content_hash},
            )
    finally:
        if own_client:
            await dp.aclose()
        await engine.dispose()
    return content_hash
