"""Database access for tenant hook bindings."""

import uuid
from dataclasses import dataclass
from datetime import datetime
from typing import Any, cast

from sqlalchemy import RowMapping, func, select, update
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.modules.schemas import ModuleStatus
from controlplane.tables import bindings, modules


@dataclass(frozen=True)
class BindingRow:
    tenant_id: uuid.UUID
    hook_id: uuid.UUID
    active_module_id: uuid.UUID | None
    active_module_hash: str | None
    config: dict[str, str]
    config_version: int
    updated_at: datetime


@dataclass(frozen=True)
class ModuleCandidate:
    id: uuid.UUID
    content_hash: str
    status: ModuleStatus


def _to_binding(m: RowMapping, active_module_hash: str | None) -> BindingRow:
    return BindingRow(
        tenant_id=m["tenant_id"],
        hook_id=m["hook_id"],
        active_module_id=m["active_module_id"],
        active_module_hash=active_module_hash,
        config=cast(dict[str, str], m["config"]),
        config_version=m["config_version"],
        updated_at=m["updated_at"],
    )


async def get_module_candidate(
    conn: AsyncConnection,
    module_id: uuid.UUID,
    tenant_id: uuid.UUID,
    hook_id: uuid.UUID,
) -> ModuleCandidate | None:
    query = select(modules.c.id, modules.c.content_hash, modules.c.status).where(
        modules.c.id == module_id,
        modules.c.tenant_id == tenant_id,
        modules.c.hook_id == hook_id,
    )
    m = (await conn.execute(query)).mappings().first()
    if m is None:
        return None
    return ModuleCandidate(
        id=m["id"],
        content_hash=m["content_hash"],
        status=cast(ModuleStatus, m["status"]),
    )


async def ensure_binding(conn: AsyncConnection, tenant_id: uuid.UUID, hook_id: uuid.UUID) -> bool:
    """Creates the initial empty row; returns whether this call inserted it."""
    query = (
        insert(bindings)
        .values(tenant_id=tenant_id, hook_id=hook_id)
        .on_conflict_do_nothing(index_elements=[bindings.c.tenant_id, bindings.c.hook_id])
        .returning(bindings.c.tenant_id)
    )
    return (await conn.execute(query)).first() is not None


async def get_binding(
    conn: AsyncConnection,
    tenant_id: uuid.UUID,
    hook_id: uuid.UUID,
    *,
    for_update: bool = False,
) -> BindingRow:
    query = (
        select(
            bindings.c.tenant_id,
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
    if for_update:
        query = query.with_for_update(of=bindings)
    m = (await conn.execute(query)).mappings().one()
    return _to_binding(m, m["active_module_hash"])


async def update_binding(
    conn: AsyncConnection,
    tenant_id: uuid.UUID,
    hook_id: uuid.UUID,
    changes: dict[str, Any],
    active_module_hash: str | None,
) -> BindingRow:
    query = (
        update(bindings)
        .where(bindings.c.tenant_id == tenant_id, bindings.c.hook_id == hook_id)
        .values(**changes, updated_at=func.now())
        .returning(*bindings.c)
    )
    m = (await conn.execute(query)).mappings().one()
    return _to_binding(m, active_module_hash)
