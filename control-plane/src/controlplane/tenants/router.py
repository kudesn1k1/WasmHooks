from fastapi import APIRouter, Depends

from controlplane.auth.deps import OperatorAuth
from controlplane.tenants.schemas import Tenant, TenantCreate, TenantList, TenantPatch
from controlplane.tenants.service import TenantService


def build_router(service: TenantService, operator: OperatorAuth) -> APIRouter:
    router = APIRouter(prefix="/api/v1/tenants", tags=["tenants"], dependencies=[Depends(operator)])

    @router.get("", response_model=TenantList, status_code=200, summary="Список тенантов")
    async def list_tenants() -> TenantList:
        return TenantList(items=await service.list_all())

    @router.post("", response_model=Tenant, status_code=201, summary="Создать тенанта")
    async def create_tenant(body: TenantCreate) -> Tenant:
        return await service.create(body)

    @router.get(
        "/{external_id}",
        response_model=Tenant,
        status_code=200,
        summary="Получить тенанта",
    )
    async def get_tenant(external_id: str) -> Tenant:
        return await service.get(external_id)

    @router.patch(
        "/{external_id}",
        response_model=Tenant,
        status_code=200,
        summary="Изменить тенанта",
        description="Меняет имя и/или квоты; external_id неизменяем.",
    )
    async def patch_tenant(external_id: str, body: TenantPatch) -> Tenant:
        return await service.patch(external_id, body)

    return router
