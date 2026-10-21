"""Operator tenant management and snapshot propagation rules."""

from typing import Any

from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.configstate.version import bump_config_version
from controlplane.errors import ProblemError
from controlplane.tenants import repo
from controlplane.tenants.schemas import Tenant, TenantCreate, TenantPatch

NOT_FOUND = "Тенант не найден"


def _to_tenant(row: repo.TenantRow) -> Tenant:
    return Tenant(
        external_id=row.external_id,
        name=row.name,
        rate_limit_rps=row.rate_limit_rps,
        concurrency_limit=row.concurrency_limit,
        created_at=row.created_at,
        updated_at=row.updated_at,
    )


class TenantService:
    def __init__(self, engine: AsyncEngine) -> None:
        self._engine = engine

    async def list_all(self) -> list[Tenant]:
        async with self._engine.connect() as conn:
            return [_to_tenant(row) for row in await repo.list_tenants(conn)]

    async def get(self, external_id: str) -> Tenant:
        async with self._engine.connect() as conn:
            row = await repo.get_tenant(conn, external_id)
        if row is None:
            raise ProblemError(404, NOT_FOUND, external_id)
        return _to_tenant(row)

    async def create(self, data: TenantCreate) -> Tenant:
        async with self._engine.begin() as conn:
            row = await repo.insert_tenant(
                conn,
                data.external_id,
                data.name,
                data.rate_limit_rps,
                data.concurrency_limit,
            )
            if row is None:
                raise ProblemError(409, "Тенант с таким external_id уже есть", data.external_id)
            await bump_config_version(conn, "tenant.created", {"external_id": row.external_id})
        return _to_tenant(row)

    async def patch(self, external_id: str, data: TenantPatch) -> Tenant:
        requested = data.model_dump(exclude_none=True)
        async with self._engine.begin() as conn:
            current = await repo.get_tenant(conn, external_id, for_update=True)
            if current is None:
                raise ProblemError(404, NOT_FOUND, external_id)

            changes: dict[str, Any] = {
                field: value
                for field, value in requested.items()
                if getattr(current, field) != value
            }
            if not changes:
                return _to_tenant(current)

            updated = await repo.update_tenant(conn, current.id, changes)
            snapshot_fields = {"rate_limit_rps", "concurrency_limit"}
            if snapshot_fields & changes.keys():
                await bump_config_version(
                    conn, "tenant.updated", {"external_id": updated.external_id}
                )
        return _to_tenant(updated)
