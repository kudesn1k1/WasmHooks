"""Database access for tenants; callers own transaction boundaries."""

import uuid
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from sqlalchemy import RowMapping, func, select, update
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.tables import tenants


@dataclass(frozen=True)
class TenantRow:
    id: uuid.UUID
    external_id: str
    name: str
    rate_limit_rps: int
    concurrency_limit: int
    created_at: datetime
    updated_at: datetime


def _to_row(m: RowMapping) -> TenantRow:
    return TenantRow(
        id=m["id"],
        external_id=m["external_id"],
        name=m["name"],
        rate_limit_rps=m["rate_limit_rps"],
        concurrency_limit=m["concurrency_limit"],
        created_at=m["created_at"],
        updated_at=m["updated_at"],
    )


async def list_tenants(conn: AsyncConnection) -> list[TenantRow]:
    result = await conn.execute(select(tenants).order_by(tenants.c.external_id))
    return [_to_row(m) for m in result.mappings()]


async def get_tenant(
    conn: AsyncConnection, external_id: str, *, for_update: bool = False
) -> TenantRow | None:
    query = select(tenants).where(tenants.c.external_id == external_id)
    if for_update:
        query = query.with_for_update()
    m = (await conn.execute(query)).mappings().first()
    return None if m is None else _to_row(m)


async def insert_tenant(
    conn: AsyncConnection,
    external_id: str,
    name: str,
    rate_limit_rps: int,
    concurrency_limit: int,
) -> TenantRow | None:
    """Inserts a tenant; returns None when external_id is already taken."""
    query = (
        insert(tenants)
        .values(
            external_id=external_id,
            name=name,
            rate_limit_rps=rate_limit_rps,
            concurrency_limit=concurrency_limit,
        )
        .on_conflict_do_nothing(index_elements=[tenants.c.external_id])
        .returning(*tenants.c)
    )
    m = (await conn.execute(query)).mappings().first()
    return None if m is None else _to_row(m)


async def update_tenant(
    conn: AsyncConnection, tenant_id: uuid.UUID, changes: dict[str, Any]
) -> TenantRow:
    query = (
        update(tenants)
        .where(tenants.c.id == tenant_id)
        .values(**changes, updated_at=func.now())
        .returning(*tenants.c)
    )
    return _to_row((await conn.execute(query)).mappings().one())
