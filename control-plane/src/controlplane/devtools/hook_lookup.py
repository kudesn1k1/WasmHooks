"""Reads a hook straight from the table and shapes it as the snapshot does."""

import uuid

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.configstate.schemas import HookDefOut
from controlplane.tables import hooks


async def load_hook(conn: AsyncConnection, name: str) -> tuple[uuid.UUID, HookDefOut]:
    row = (await conn.execute(select(hooks).where(hooks.c.name == name))).mappings().first()
    if row is None:
        raise ValueError(f"hook {name!r} does not exist")
    return uuid.UUID(str(row["id"])), HookDefOut(
        name=row["name"],
        def_version=row["def_version"],
        input_schema=row["input_schema"],
        output_schema=row["output_schema"],
        timeout_ms=row["timeout_ms"],
        memory_max_pages=row["memory_max_pages"],
        allowed_host_functions=list(row["allowed_host_functions"]),
        allowed_effect_types=list(row["allowed_effect_types"]),
        sample_input=row["sample_input"],
    )
