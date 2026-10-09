"""HTTP layer of the operator hooks API: no logic, only shapes and status codes."""

from fastapi import APIRouter, Depends

from controlplane.auth.deps import OperatorAuth
from controlplane.hooks.schemas import Hook, HookCreate, HookList, HookSpec
from controlplane.hooks.service import HookService


def build_router(service: HookService, operator: OperatorAuth) -> APIRouter:
    router = APIRouter(prefix="/api/v1/hooks", tags=["hooks"], dependencies=[Depends(operator)])

    @router.get("", response_model=HookList, summary="Список хуков")
    async def list_hooks() -> HookList:
        return HookList(items=await service.list_all())

    @router.post("", status_code=201, response_model=Hook, summary="Создать хук")
    async def create_hook(body: HookCreate) -> Hook:
        return await service.create(body)

    @router.get("/{name}", response_model=Hook, summary="Определение хука")
    async def get_hook(name: str) -> Hook:
        return await service.get(name)

    @router.put(
        "/{name}",
        response_model=Hook,
        summary="Заменить определение хука",
        description=(
            "Полная замена определения; имя неизменно. Без изменений — ответ без роста "
            "def_version. Списки разрешённых host-функций и эффектов могут только расти."
        ),
    )
    async def replace_hook(name: str, body: HookSpec) -> Hook:
        return await service.replace(name, body)

    return router
