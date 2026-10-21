"""Installation API-key lifecycle for the operator API."""

from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.apikeys import repo
from controlplane.apikeys.schemas import ApiKey, ApiKeyCreated
from controlplane.auth.keys import create_api_key
from controlplane.configstate.version import bump_config_version
from controlplane.errors import ProblemError


def _to_api_key(row: repo.ApiKeyRow) -> ApiKey:
    return ApiKey(
        id=row.id,
        name=row.name,
        created_at=row.created_at,
        revoked_at=row.revoked_at,
    )


class ApiKeyService:
    def __init__(self, engine: AsyncEngine) -> None:
        self._engine = engine

    async def list_all(self) -> list[ApiKey]:
        async with self._engine.connect() as conn:
            return [_to_api_key(row) for row in await repo.list_api_keys(conn)]

    async def create(self, name: str) -> ApiKeyCreated:
        try:
            async with self._engine.begin() as conn:
                if await repo.name_exists(conn, name):
                    raise ProblemError(409, "Ключ с таким именем уже есть", name)
                created_at = await repo.transaction_timestamp(conn)
                # This helper inserts the key and bumps the snapshot version;
                # keep it last so the global version lock is held briefly.
                key = await create_api_key(conn, name)
        except IntegrityError as exc:
            # A concurrent request may insert the same unique name after our check.
            raise ProblemError(409, "Ключ с таким именем уже есть", name) from exc
        return ApiKeyCreated(
            id=key.id,
            name=key.name,
            token=key.token,
            created_at=created_at,
            revoked_at=None,
        )

    async def revoke(self, key_id: str) -> None:
        async with self._engine.begin() as conn:
            current = await repo.get_api_key(conn, key_id, for_update=True)
            if current is None:
                raise ProblemError(404, "Ключ не найден", key_id)
            if current.revoked_at is not None:
                return
            await repo.revoke_api_key(conn, key_id)
            await bump_config_version(conn, "api_key.revoked", {"id": key_id})
