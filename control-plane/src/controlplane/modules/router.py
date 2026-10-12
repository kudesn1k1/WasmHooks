from fastapi import APIRouter, Depends, UploadFile

from controlplane.auth.deps import TenantAuth
from controlplane.modules.schemas import Module, ModuleList

_NOTE = "Реализуется в T3, см. docs/handoff/backend.md."


def build_router(tenant: TenantAuth) -> APIRouter:
    router = APIRouter(
        prefix="/api/v1/tenant/hooks", tags=["tenant"], dependencies=[Depends(tenant)]
    )

    @router.get(
        "/{hook}/modules",
        response_model=ModuleList,
        status_code=200,
        summary="Версии модуля хука",
        description=_NOTE,
    )
    async def list_modules(hook: str) -> ModuleList:
        raise NotImplementedError("T3")

    @router.post(
        "/{hook}/modules",
        response_model=Module,
        status_code=201,
        summary="Загрузить версию модуля",
        description="Тело multipart/form-data, поле file. " + _NOTE,
    )
    async def upload_module(hook: str, file: UploadFile) -> Module:
        raise NotImplementedError("T3")

    return router
