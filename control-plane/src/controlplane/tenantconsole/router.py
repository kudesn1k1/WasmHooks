from typing import Annotated

from fastapi import APIRouter, Depends

from controlplane.auth.deps import TenantAuth, TenantPrincipal
from controlplane.tenantconsole.schemas import TenantHook, TenantHookList
from controlplane.tenantconsole.service import TenantConsoleService


def build_router(service: TenantConsoleService, tenant: TenantAuth) -> APIRouter:
    router = APIRouter(prefix="/api/v1/tenant/hooks", tags=["tenant"])

    @router.get(
        "",
        response_model=TenantHookList,
        status_code=200,
        summary="Хуки тенанта",
    )
    async def list_hooks(
        principal: Annotated[TenantPrincipal, Depends(tenant)],
    ) -> TenantHookList:
        return TenantHookList(items=await service.list_all(principal.external_id))

    @router.get(
        "/{hook}",
        response_model=TenantHook,
        status_code=200,
        summary="Хук тенанта",
    )
    async def get_hook(
        hook: str, principal: Annotated[TenantPrincipal, Depends(tenant)]
    ) -> TenantHook:
        return await service.get(principal.external_id, hook)

    return router
