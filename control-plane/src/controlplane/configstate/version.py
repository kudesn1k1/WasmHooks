from collections.abc import Mapping
from typing import Any

from sqlalchemy import insert, update
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.tables import config_changes, config_state


async def bump_config_version(conn: AsyncConnection, kind: str, payload: Mapping[str, Any]) -> int:
    """Increments the snapshot version inside the caller's transaction.

    Call it exactly once from every operation that changes what the snapshot
    shows (hooks, tenants, bindings, API keys), in the same transaction.

    The row lock on config_state is held until commit, so versions are handed
    out in commit order. A sequence would allocate them at insert time: a
    change could commit after a reader saw a higher version and never reach
    the data plane.
    """
    result = await conn.execute(
        update(config_state)
        .where(config_state.c.id == 1)
        .values(version=config_state.c.version + 1)
        .returning(config_state.c.version)
    )
    version = int(result.scalar_one())
    await conn.execute(
        insert(config_changes).values(version=version, kind=kind, payload=dict(payload))
    )
    return version
