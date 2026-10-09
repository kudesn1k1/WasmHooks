"""Database access for hooks. Functions take the caller's connection, so the
caller decides the transaction boundaries."""

import uuid
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from sqlalchemy import RowMapping, func, select, update
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.hooks.schemas import HookSpec
from controlplane.tables import hooks


@dataclass(frozen=True)
class HookRow:
    id: uuid.UUID
    name: str
    def_version: int
    spec: HookSpec
    created_at: datetime
    updated_at: datetime


def _spec_values(spec: HookSpec) -> dict[str, Any]:
    return spec.model_dump()


def _to_row(m: RowMapping) -> HookRow:
    return HookRow(
        id=m["id"],
        name=m["name"],
        def_version=m["def_version"],
        spec=HookSpec(
            input_schema=m["input_schema"],
            output_schema=m["output_schema"],
            timeout_ms=m["timeout_ms"],
            memory_max_pages=m["memory_max_pages"],
            allowed_host_functions=m["allowed_host_functions"],
            allowed_effect_types=m["allowed_effect_types"],
            sample_input=m["sample_input"],
        ),
        created_at=m["created_at"],
        updated_at=m["updated_at"],
    )


async def list_hooks(conn: AsyncConnection) -> list[HookRow]:
    result = await conn.execute(select(hooks).order_by(hooks.c.name))
    return [_to_row(m) for m in result.mappings()]


async def get_hook(conn: AsyncConnection, name: str, *, for_update: bool = False) -> HookRow | None:
    query = select(hooks).where(hooks.c.name == name)
    if for_update:
        query = query.with_for_update()
    m = (await conn.execute(query)).mappings().first()
    return None if m is None else _to_row(m)


async def insert_hook(conn: AsyncConnection, name: str, spec: HookSpec) -> HookRow | None:
    """Inserts a hook at def_version 1; None if the name is taken."""
    query = (
        insert(hooks)
        .values(name=name, def_version=1, **_spec_values(spec))
        .on_conflict_do_nothing(index_elements=[hooks.c.name])
        .returning(*hooks.c)
    )
    m = (await conn.execute(query)).mappings().first()
    return None if m is None else _to_row(m)


async def update_hook(
    conn: AsyncConnection, hook_id: uuid.UUID, spec: HookSpec, def_version: int
) -> HookRow:
    query = (
        update(hooks)
        .where(hooks.c.id == hook_id)
        .values(def_version=def_version, updated_at=func.now(), **_spec_values(spec))
        .returning(*hooks.c)
    )
    return _to_row((await conn.execute(query)).mappings().one())
