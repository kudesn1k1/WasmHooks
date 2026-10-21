from typing import Annotated

from fastapi import APIRouter, Depends, UploadFile

from controlplane.auth.deps import TenantAuth, TenantPrincipal
from controlplane.modules.schemas import Module, ModuleList
from controlplane.modules.service import ModuleService


def build_router(service: ModuleService, tenant: TenantAuth) -> APIRouter:
    router = APIRouter(prefix="/api/v1/tenant/hooks", tags=["tenant"])

    @router.get(
        "/{hook}/modules",
        response_model=ModuleList,
        status_code=200,
        summary="Версии модуля хука",
    )
    async def list_modules(
        hook: str, principal: Annotated[TenantPrincipal, Depends(tenant)]
    ) -> ModuleList:
        return ModuleList(items=await service.list_all(principal.external_id, hook))

    @router.post(
        "/{hook}/modules",
        response_model=Module,
        status_code=201,
        summary="Загрузить версию модуля",
        description="Тело multipart/form-data, поле file.",
    )
    async def upload_module(
        hook: str,
        file: UploadFile,
        principal: Annotated[TenantPrincipal, Depends(tenant)],
    ) -> Module:
        return await service.upload(principal.external_id, hook, file)

    return router
