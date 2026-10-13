"""Database access for tenant module versions."""

import uuid
from dataclasses import dataclass
from datetime import datetime
from typing import cast

from sqlalchemy import RowMapping, select, update
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.dpclient.schemas import ValidationReport
from controlplane.modules.schemas import ModuleStatus
from controlplane.tables import bindings, hooks, modules


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


@dataclass(frozen=True)
class ValidationTarget:
    id: uuid.UUID
    tenant_id: uuid.UUID
    hook_id: uuid.UUID
    hook: str
    content_hash: str
    status: ModuleStatus


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


async def list_pending_module_ids(conn: AsyncConnection) -> list[uuid.UUID]:
    query = (
        select(modules.c.id)
        .where(modules.c.status.in_(["uploaded", "validating"]))
        .order_by(modules.c.uploaded_at, modules.c.id)
    )
    return list((await conn.execute(query)).scalars())


async def get_validation_target(
    conn: AsyncConnection, module_id: uuid.UUID, *, for_update: bool = False
) -> ValidationTarget | None:
    query = (
        select(
            modules.c.id,
            modules.c.tenant_id,
            modules.c.hook_id,
            hooks.c.name.label("hook"),
            modules.c.content_hash,
            modules.c.status,
        )
        .join(hooks, hooks.c.id == modules.c.hook_id)
        .where(modules.c.id == module_id)
    )
    if for_update:
        query = query.with_for_update(of=modules)
    m = (await conn.execute(query)).mappings().first()
    if m is None:
        return None
    return ValidationTarget(
        id=m["id"],
        tenant_id=m["tenant_id"],
        hook_id=m["hook_id"],
        hook=m["hook"],
        content_hash=m["content_hash"],
        status=cast(ModuleStatus, m["status"]),
    )


async def mark_validating(conn: AsyncConnection, module_id: uuid.UUID) -> None:
    await conn.execute(
        update(modules)
        .where(modules.c.id == module_id, modules.c.status == "uploaded")
        .values(status="validating")
    )


async def binding_config(
    conn: AsyncConnection, tenant_id: uuid.UUID, hook_id: uuid.UUID
) -> dict[str, str] | None:
    value = (
        await conn.execute(
            select(bindings.c.config).where(
                bindings.c.tenant_id == tenant_id, bindings.c.hook_id == hook_id
            )
        )
    ).scalar_one_or_none()
    return None if value is None else cast(dict[str, str], value)


async def finish_validation(
    conn: AsyncConnection,
    module_id: uuid.UUID,
    status: ModuleStatus,
    report: ValidationReport,
) -> None:
    await conn.execute(
        update(modules)
        .where(modules.c.id == module_id, modules.c.status == "validating")
        .values(
            status=status,
            validation_report=report.model_dump(mode="json", exclude_none=True),
        )
    )
