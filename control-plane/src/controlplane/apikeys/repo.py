"""Database access for installation API keys."""

from dataclasses import dataclass
from datetime import datetime

from sqlalchemy import RowMapping, func, select, update
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.tables import api_keys


@dataclass(frozen=True)
class ApiKeyRow:
    id: str
    name: str
    created_at: datetime
    revoked_at: datetime | None


def _to_row(m: RowMapping) -> ApiKeyRow:
    return ApiKeyRow(
        id=m["id"],
        name=m["name"],
        created_at=m["created_at"],
        revoked_at=m["revoked_at"],
    )


async def list_api_keys(conn: AsyncConnection) -> list[ApiKeyRow]:
    query = select(
        api_keys.c.id,
        api_keys.c.name,
        api_keys.c.created_at,
        api_keys.c.revoked_at,
    ).order_by(api_keys.c.created_at, api_keys.c.id)
    return [_to_row(m) for m in (await conn.execute(query)).mappings()]


async def get_api_key(
    conn: AsyncConnection, key_id: str, *, for_update: bool = False
) -> ApiKeyRow | None:
    query = select(
        api_keys.c.id,
        api_keys.c.name,
        api_keys.c.created_at,
        api_keys.c.revoked_at,
    ).where(api_keys.c.id == key_id)
    if for_update:
        query = query.with_for_update()
    m = (await conn.execute(query)).mappings().first()
    return None if m is None else _to_row(m)


async def name_exists(conn: AsyncConnection, name: str) -> bool:
    return (
        await conn.execute(select(api_keys.c.id).where(api_keys.c.name == name))
    ).first() is not None


async def transaction_timestamp(conn: AsyncConnection) -> datetime:
    """Returns the value PostgreSQL uses for created_at defaults in this transaction."""
    return (await conn.execute(select(func.now()))).scalar_one()


async def revoke_api_key(conn: AsyncConnection, key_id: str) -> ApiKeyRow:
    query = (
        update(api_keys)
        .where(api_keys.c.id == key_id)
        .values(revoked_at=func.now())
        .returning(
            api_keys.c.id,
            api_keys.c.name,
            api_keys.c.created_at,
            api_keys.c.revoked_at,
        )
    )
    return _to_row((await conn.execute(query)).mappings().one())
