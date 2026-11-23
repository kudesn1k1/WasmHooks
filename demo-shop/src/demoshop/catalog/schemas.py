from pydantic import BaseModel, ConfigDict, Field


class Shop(BaseModel):
    """A merchant's shop on the marketplace: one tenant of the platform."""

    model_config = ConfigDict(frozen=True)

    id: str = Field(
        description=(
            "Идентификатор магазина. Он же external_id мерчанта в платформе: под ним "
            "вызывается хук и открывается консоль скриптов."
        )
    )
    name: str
    description: str


class ShopList(BaseModel):
    items: list[Shop]


class Product(BaseModel):
    model_config = ConfigDict(frozen=True)

    id: str = Field(description="Идентификатор товара, его передают в чекаут.")
    shop_id: str = Field(description="Магазин, который продаёт товар.")
    name: str
    description: str
    price: int = Field(ge=0, description="Цена за штуку в копейках.")


class ProductList(BaseModel):
    items: list[Product]


class Customer(BaseModel):
    """A demo buyer: the marketplace has no accounts, the storefront picks who buys."""

    model_config = ConfigDict(frozen=True)

    id: str = Field(description="Идентификатор покупателя, уходит в хук как customer.id.")
    name: str
    lifetime_spend: int = Field(
        ge=0, description="Сумма прошлых покупок в копейках, уходит в хук в рублях."
    )


class CustomerList(BaseModel):
    items: list[Customer]
