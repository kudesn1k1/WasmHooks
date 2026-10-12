"""Database access for tenant module versions."""

import uuid
from dataclasses import dataclass
from datetime import datetime
from typing import cast

from sqlalchemy import RowMapping, select
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.dpclient.schemas import ValidationReport
from controlplane.modules.schemas import ModuleStatus
from controlplane.tables import modules


@dataclass(frozen=True)
class ModuleRow:
    id: uuid.UUID
    tenant_id: uuid.UUID
    hook_id: uuid.UUID
    hook: str
    content_hash: str
    size_bytes: int
    status: ModuleStatus
    validation_report: ValidationReport | None
    uploaded_at: datetime


def _to_row(m: RowMapping, hook: str) -> ModuleRow:
    raw_report = m["validation_report"]
    return ModuleRow(
        id=m["id"],
        tenant_id=m["tenant_id"],
        hook_id=m["hook_id"],
        hook=hook,
        content_hash=m["content_hash"],
        size_bytes=m["size_bytes"],
        status=cast(ModuleStatus, m["status"]),
        validation_report=(
            None if raw_report is None else ValidationReport.model_validate(raw_report)
        ),
        uploaded_at=m["uploaded_at"],
    )


async def list_modules(
    conn: AsyncConnection, tenant_id: uuid.UUID, hook_id: uuid.UUID, hook: str
) -> list[ModuleRow]:
    query = (
        select(modules)
        .where(modules.c.tenant_id == tenant_id, modules.c.hook_id == hook_id)
        .order_by(modules.c.uploaded_at.desc(), modules.c.id.desc())
    )
    return [_to_row(m, hook) for m in (await conn.execute(query)).mappings()]


async def insert_module(
    conn: AsyncConnection,
    tenant_id: uuid.UUID,
    hook_id: uuid.UUID,
    hook: str,
    content_hash: str,
    size_bytes: int,
) -> ModuleRow | None:
    """Inserts an uploaded version; None means this tenant already uploaded it."""
    query = (
        insert(modules)
        .values(
            tenant_id=tenant_id,
            hook_id=hook_id,
            content_hash=content_hash,
            size_bytes=size_bytes,
            status="uploaded",
        )
        .on_conflict_do_nothing(
            index_elements=[modules.c.tenant_id, modules.c.hook_id, modules.c.content_hash]
        )
        .returning(*modules.c)
    )
    m = (await conn.execute(query)).mappings().first()
    return None if m is None else _to_row(m, hook)
