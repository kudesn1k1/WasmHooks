from enum import StrEnum

from pydantic import BaseModel, ConfigDict, Field


class CartItem(BaseModel):
    model_config = ConfigDict(extra="forbid")

    product_id: str
    quantity: int = Field(ge=1, le=99)


class CheckoutRequest(BaseModel):
    """The cart lives in the storefront; the shop prices it from its own catalog."""

    model_config = ConfigDict(extra="forbid")

    customer_id: str
    items: list[CartItem] = Field(min_length=1, max_length=50)


class DiscountOutcome(StrEnum):
    OK = "ok"
    NO_HANDLER = "no_handler"
    TIMEOUT = "timeout"
    HANDLER_ERROR = "handler_error"
    QUOTA_EXCEEDED = "quota_exceeded"
    UNAVAILABLE = "unavailable"
    ERROR = "error"


class Discount(BaseModel):
    percent: int = Field(ge=0, le=100, description="Скидка в процентах.")
    amount: int = Field(ge=0, description="Скидка в копейках.")
    reason: str | None = Field(
        description="Причина от скрипта мерчанта; null, если скидка по умолчанию."
    )
    outcome: DiscountOutcome = Field(
        description=(
            "Исход вызова хука checkout.discount (api/invoke.openapi.yaml). `error` — "
            "исхода нет: сеть, истёк дедлайн или data plane отклонил запрос."
        )
    )
    fallback: bool = Field(
        description=(
            "true — применена скидка по умолчанию (0): исход не `ok` или скрипт "
            "вернул результат не по схеме. Это нормальный путь, а не ошибка."
        )
    )


class CheckoutLine(BaseModel):
    product_id: str
    name: str
    unit_price: int = Field(ge=0, description="Цена за штуку в копейках.")
    quantity: int = Field(ge=1)
    line_total: int = Field(ge=0, description="Стоимость строки в копейках.")


class Checkout(BaseModel):
    shop_id: str
    customer_id: str
    lines: list[CheckoutLine]
    subtotal: int = Field(ge=0, description="Сумма без скидки в копейках.")
    discount: Discount
    total: int = Field(ge=0, description="Итог к оплате в копейках.")
