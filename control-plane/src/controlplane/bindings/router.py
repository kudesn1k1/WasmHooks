from fastapi import APIRouter, Depends

from controlplane.auth.deps import TenantAuth
from controlplane.bindings.schemas import BindingUpdate
from controlplane.tenantconsole.schemas import TenantBinding


def build_router(tenant: TenantAuth) -> APIRouter:
    router = APIRouter(
        prefix="/api/v1/tenant/hooks", tags=["tenant"], dependencies=[Depends(tenant)]
    )

    @router.put(
        "/{hook}/binding",
        response_model=TenantBinding,
        status_code=200,
        summary="Изменить привязку хука",
        description="Активная версия и/или конфиг. Реализуется в T5, см. docs/handoff/backend.md.",
    )
    async def update_binding(hook: str, body: BindingUpdate) -> TenantBinding:
        raise NotImplementedError("T5")

    return router
