import logging

from fastapi import APIRouter

from demoshop.catalog.service import Catalog
from demoshop.console.client import ConsoleSession, ConsoleUnavailable, ControlPlaneClient
from demoshop.errors import ProblemError, problem_doc

logger = logging.getLogger(__name__)


def build_router(control_plane: ControlPlaneClient, catalog: Catalog) -> APIRouter:
    router = APIRouter(prefix="/api/v1", tags=["merchant console"])

    @router.post(
        "/shops/{shop_id}/console-sessions",
        response_model=ConsoleSession,
        status_code=201,
        summary="Открыть консоль скриптов мерчанта",
        description=(
            "Кнопка «Мои скрипты». Магазин с ключом установки регистрирует своего мерчанта "
            "тенантом платформы, если его там ещё нет, и выписывает токен консоли тенанта; "
            "витрина открывает `/tenant#token=<token>`. Учётных записей в демо нет, "
            "поэтому токен получает любой посетитель витрины."
        ),
        responses={
            404: problem_doc("Магазин не найден"),
            503: problem_doc("Control plane не выдал токен"),
        },
    )
    async def create_console_session(shop_id: str) -> ConsoleSession:
        shop = catalog.shop(shop_id)
        try:
            await control_plane.ensure_tenant(shop.id, shop.name)
            return await control_plane.console_session(shop.id)
        except ConsoleUnavailable as exc:
            # Any visitor can press the button: the details stay in the log.
            logger.error("console session for %s failed: %s", shop.id, exc)
            raise ProblemError(503, "Консоль скриптов недоступна") from exc

    return router
