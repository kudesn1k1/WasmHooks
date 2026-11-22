from fastapi import APIRouter

from demoshop.checkout.schemas import Checkout, CheckoutRequest
from demoshop.checkout.service import CheckoutService
from demoshop.errors import problem_doc


def build_router(service: CheckoutService) -> APIRouter:
    router = APIRouter(prefix="/api/v1", tags=["checkout"])

    @router.post(
        "/shops/{shop_id}/checkout",
        response_model=Checkout,
        status_code=200,
        summary="Рассчитать заказ со скидкой",
        description=(
            "Считает корзину по ценам каталога магазина и спрашивает скидку у скрипта его "
            "мерчанта (хук checkout.discount). Любой исход хука, кроме `ok`, даёт скидку 0 "
            "и `fallback: true`: чекаут от скрипта не ломается. Заказ не сохраняется, "
            "оплаты нет."
        ),
        # 422 replaces FastAPI's own HTTPValidationError shape, which this API never returns.
        responses={
            404: problem_doc("Магазин, покупатель или товар не найден"),
            422: problem_doc("Некорректное тело запроса"),
        },
    )
    async def checkout(shop_id: str, body: CheckoutRequest) -> Checkout:
        return await service.checkout(shop_id, body)

    return router
