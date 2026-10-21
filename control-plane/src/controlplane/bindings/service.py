"""Activation, rollback and configuration of tenant hook bindings."""

import uuid
from typing import Any

from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.bindings import repo
from controlplane.bindings.schemas import BindingUpdate
from controlplane.configstate.version import bump_config_version
from controlplane.errors import ProblemError
from controlplane.hooks import repo as hooks_repo
from controlplane.hooks.service import NOT_FOUND as HOOK_NOT_FOUND
from controlplane.tenantconsole.schemas import TenantBinding
from controlplane.tenants import repo as tenants_repo


def _to_binding(row: repo.BindingRow) -> TenantBinding:
    return TenantBinding(
        active_module_id=None if row.active_module_id is None else str(row.active_module_id),
        active_module_hash=row.active_module_hash,
        config=row.config,
        config_version=row.config_version,
        updated_at=row.updated_at,
    )


class BindingService:
    def __init__(self, engine: AsyncEngine) -> None:
        self._engine = engine

    async def update(self, external_id: str, hook_name: str, data: BindingUpdate) -> TenantBinding:
        if data.active_module_id is None and data.config is None:
            raise ProblemError(400, "Нечего менять")

        module_id = self._module_id(data.active_module_id)
        async with self._engine.begin() as conn:
            tenant = await tenants_repo.get_tenant(conn, external_id)
            hook = await hooks_repo.get_hook(conn, hook_name)
            if hook is None:
                raise ProblemError(404, HOOK_NOT_FOUND, hook_name)
            if tenant is None:
                raise ProblemError(404, "Тенант не найден", external_id)

            candidate: repo.ModuleCandidate | None = None
            if module_id is not None:
                candidate = await repo.get_module_candidate(conn, module_id, tenant.id, hook.id)
                if candidate is None:
                    raise ProblemError(404, "Версия не найдена", data.active_module_id)
                if candidate.status != "validated":
                    raise ProblemError(
                        409,
                        "Активировать можно только проверенную версию",
                        f"Статус: {candidate.status}",
                    )

            created = await repo.ensure_binding(conn, tenant.id, hook.id)
            current = await repo.get_binding(conn, tenant.id, hook.id, for_update=True)
            changes: dict[str, Any] = {}
            active_hash = current.active_module_hash

            if candidate is not None and candidate.id != current.active_module_id:
                changes["active_module_id"] = candidate.id
                active_hash = candidate.content_hash
            if data.config is not None and data.config != current.config:
                changes["config"] = data.config
                if not created:
                    changes["config_version"] = current.config_version + 1

            if not created and not changes:
                return _to_binding(current)
            updated = (
                await repo.update_binding(conn, tenant.id, hook.id, changes, active_hash)
                if changes
                else current
            )
            await bump_config_version(
                conn,
                "binding.updated",
                {"tenant": external_id, "hook": hook_name},
            )
        return _to_binding(updated)

    @staticmethod
    def _module_id(raw: str | None) -> uuid.UUID | None:
        if raw is None:
            return None
        try:
            return uuid.UUID(raw)
        except ValueError as exc:
            raise ProblemError(404, "Версия не найдена", raw) from exc
