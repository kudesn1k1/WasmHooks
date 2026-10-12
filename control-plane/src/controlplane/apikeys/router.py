from fastapi import APIRouter, Depends

from controlplane.apikeys.schemas import ApiKeyCreate, ApiKeyCreated, ApiKeyList
from controlplane.auth.deps import OperatorAuth

_NOTE = "Реализуется в T2, см. docs/handoff/backend.md."


def build_router(operator: OperatorAuth) -> APIRouter:
    router = APIRouter(
        prefix="/api/v1/api-keys", tags=["api-keys"], dependencies=[Depends(operator)]
    )

    @router.get(
        "", response_model=ApiKeyList, status_code=200, summary="Список ключей", description=_NOTE
    )
    async def list_api_keys() -> ApiKeyList:
        raise NotImplementedError("T2")

    @router.post(
        "",
        response_model=ApiKeyCreated,
        status_code=201,
        summary="Выпустить ключ",
        description=_NOTE + " Токен показывается один раз.",
    )
    async def create_api_key(body: ApiKeyCreate) -> ApiKeyCreated:
        raise NotImplementedError("T2")

    @router.delete("/{key_id}", status_code=204, summary="Отозвать ключ", description=_NOTE)
    async def revoke_api_key(key_id: str) -> None:
        raise NotImplementedError("T2")

    return router
