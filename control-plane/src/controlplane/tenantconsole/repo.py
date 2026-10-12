"""Read models for tenant-visible hook bindings."""

import uuid
from dataclasses import dataclass
from datetime import datetime
from typing import cast

from sqlalchemy import RowMapping, select
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.tables import bindings, modules


@dataclass(frozen=True)
class BindingRow:
    hook_id: uuid.UUID
    active_module_id: uuid.UUID | None
    active_module_hash: str | None
    config: dict[str, str]
    config_version: int
    updated_at: datetime


def _to_row(m: RowMapping) -> BindingRow:
    return BindingRow(
        hook_id=m["hook_id"],
        active_module_id=m["active_module_id"],
        active_module_hash=m["active_module_hash"],
        config=cast(dict[str, str], m["config"]),
        config_version=m["config_version"],
        updated_at=m["updated_at"],
    )


async def list_bindings(conn: AsyncConnection, tenant_id: uuid.UUID) -> dict[uuid.UUID, BindingRow]:
    query = (
        select(
            bindings.c.hook_id,
            bindings.c.active_module_id,
            modules.c.content_hash.label("active_module_hash"),
            bindings.c.config,
            bindings.c.config_version,
            bindings.c.updated_at,
        )
        .select_from(bindings.outerjoin(modules, modules.c.id == bindings.c.active_module_id))
        .where(bindings.c.tenant_id == tenant_id)
    )
    rows = [_to_row(m) for m in (await conn.execute(query)).mappings()]
    return {row.hook_id: row for row in rows}


async def get_binding(
    conn: AsyncConnection, tenant_id: uuid.UUID, hook_id: uuid.UUID
) -> BindingRow | None:
    query = (
        select(
            bindings.c.hook_id,
            bindings.c.active_module_id,
            modules.c.content_hash.label("active_module_hash"),
            bindings.c.config,
            bindings.c.config_version,
            bindings.c.updated_at,
        )
        .select_from(bindings.outerjoin(modules, modules.c.id == bindings.c.active_module_id))
        .where(bindings.c.tenant_id == tenant_id, bindings.c.hook_id == hook_id)
    )
    m = (await conn.execute(query)).mappings().first()
    return None if m is None else _to_row(m)
