"""Read-only tenant console view of hooks and the tenant's own bindings."""

from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.errors import ProblemError
from controlplane.hooks import repo as hooks_repo
from controlplane.hooks.service import NOT_FOUND as HOOK_NOT_FOUND
from controlplane.tenantconsole import repo
from controlplane.tenantconsole.schemas import HookView, TenantBinding, TenantHook
from controlplane.tenants import repo as tenants_repo


def _hook_view(row: hooks_repo.HookRow) -> HookView:
    return HookView(name=row.name, def_version=row.def_version, **row.spec.model_dump())


def _binding_view(row: repo.BindingRow | None) -> TenantBinding | None:
    if row is None:
        return None
    return TenantBinding(
        active_module_id=None if row.active_module_id is None else str(row.active_module_id),
        active_module_hash=row.active_module_hash,
        config=row.config,
        config_version=row.config_version,
        updated_at=row.updated_at,
    )


class TenantConsoleService:
    def __init__(self, engine: AsyncEngine) -> None:
        self._engine = engine

    async def list_all(self, external_id: str) -> list[TenantHook]:
        async with self._engine.connect() as conn:
            tenant = await tenants_repo.get_tenant(conn, external_id)
            if tenant is None:
                raise ProblemError(404, "Тенант не найден", external_id)
            hooks = await hooks_repo.list_hooks(conn)
            tenant_bindings = await repo.list_bindings(conn, tenant.id)
        return [
            TenantHook(hook=_hook_view(hook), binding=_binding_view(tenant_bindings.get(hook.id)))
            for hook in hooks
        ]

    async def get(self, external_id: str, hook_name: str) -> TenantHook:
        async with self._engine.connect() as conn:
            tenant = await tenants_repo.get_tenant(conn, external_id)
            hook = await hooks_repo.get_hook(conn, hook_name)
            if hook is None:
                raise ProblemError(404, HOOK_NOT_FOUND, hook_name)
            if tenant is None:
                raise ProblemError(404, "Тенант не найден", external_id)
            binding = await repo.get_binding(conn, tenant.id, hook.id)
        return TenantHook(hook=_hook_view(hook), binding=_binding_view(binding))
