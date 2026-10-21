from fastapi import APIRouter, Depends

from controlplane.apikeys.schemas import ApiKeyCreate, ApiKeyCreated, ApiKeyList
from controlplane.apikeys.service import ApiKeyService
from controlplane.auth.deps import OperatorAuth


def build_router(service: ApiKeyService, operator: OperatorAuth) -> APIRouter:
    router = APIRouter(
        prefix="/api/v1/api-keys", tags=["api-keys"], dependencies=[Depends(operator)]
    )

    @router.get("", response_model=ApiKeyList, status_code=200, summary="Список ключей")
    async def list_api_keys() -> ApiKeyList:
        return ApiKeyList(items=await service.list_all())

    @router.post(
        "",
        response_model=ApiKeyCreated,
        status_code=201,
        summary="Выпустить ключ",
        description="Токен показывается один раз и затем не может быть получен через API.",
    )
    async def create_api_key(body: ApiKeyCreate) -> ApiKeyCreated:
        return await service.create(body.name)

    @router.delete("/{key_id}", status_code=204, summary="Отозвать ключ")
    async def revoke_api_key(key_id: str) -> None:
        await service.revoke(key_id)

    return router
