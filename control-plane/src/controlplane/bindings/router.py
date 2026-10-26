from typing import Annotated

from fastapi import APIRouter, Depends

from controlplane.auth.deps import TenantAuth, TenantPrincipal
from controlplane.bindings.schemas import BindingUpdate
from controlplane.bindings.service import BindingService
from controlplane.tenantconsole.schemas import TenantBinding


def build_router(service: BindingService, tenant: TenantAuth) -> APIRouter:
    router = APIRouter(prefix="/api/v1/tenant/hooks", tags=["tenant"])

    @router.put(
        "/{hook}/binding",
        response_model=TenantBinding,
        status_code=200,
        summary="Изменить привязку хука",
        description="Активирует проверенную версию и/или изменяет конфиг хука.",
    )
    async def update_binding(
        hook: str,
        body: BindingUpdate,
        principal: Annotated[TenantPrincipal, Depends(tenant)],
    ) -> TenantBinding:
        return await service.update(principal.external_id, hook, body)

    return router
