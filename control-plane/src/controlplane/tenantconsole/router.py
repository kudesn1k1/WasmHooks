from fastapi import APIRouter, Depends

from controlplane.auth.deps import TenantAuth
from controlplane.tenantconsole.schemas import TenantHook, TenantHookList

_NOTE = "Реализуется в T6, см. docs/handoff/backend.md."


def build_router(tenant: TenantAuth) -> APIRouter:
    router = APIRouter(
        prefix="/api/v1/tenant/hooks", tags=["tenant"], dependencies=[Depends(tenant)]
    )

    @router.get(
        "",
        response_model=TenantHookList,
        status_code=200,
        summary="Хуки тенанта",
        description=_NOTE,
    )
    async def list_hooks() -> TenantHookList:
        raise NotImplementedError("T6")

    @router.get(
        "/{hook}",
        response_model=TenantHook,
        status_code=200,
        summary="Хук тенанта",
        description=_NOTE,
    )
    async def get_hook(hook: str) -> TenantHook:
        raise NotImplementedError("T6")

    return router
