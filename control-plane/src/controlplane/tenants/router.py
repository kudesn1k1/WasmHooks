from fastapi import APIRouter, Depends

from controlplane.auth.deps import OperatorAuth
from controlplane.tenants.schemas import Tenant, TenantCreate, TenantList, TenantPatch

_NOTE = "Реализуется в T1, см. docs/handoff/backend.md."


def build_router(operator: OperatorAuth) -> APIRouter:
    router = APIRouter(prefix="/api/v1/tenants", tags=["tenants"], dependencies=[Depends(operator)])

    @router.get(
        "", response_model=TenantList, status_code=200, summary="Список тенантов", description=_NOTE
    )
    async def list_tenants() -> TenantList:
        raise NotImplementedError("T1")

    @router.post(
        "", response_model=Tenant, status_code=201, summary="Создать тенанта", description=_NOTE
    )
    async def create_tenant(body: TenantCreate) -> Tenant:
        raise NotImplementedError("T1")

    @router.get(
        "/{external_id}",
        response_model=Tenant,
        status_code=200,
        summary="Получить тенанта",
        description=_NOTE,
    )
    async def get_tenant(external_id: str) -> Tenant:
        raise NotImplementedError("T1")

    @router.patch(
        "/{external_id}",
        response_model=Tenant,
        status_code=200,
        summary="Изменить тенанта",
        description=_NOTE,
    )
    async def patch_tenant(external_id: str, body: TenantPatch) -> Tenant:
        raise NotImplementedError("T1")

    return router
