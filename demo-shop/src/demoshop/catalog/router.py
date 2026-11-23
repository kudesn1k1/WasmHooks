from fastapi import APIRouter

from demoshop.catalog.schemas import CustomerList, ProductList, ShopList
from demoshop.catalog.service import Catalog
from demoshop.errors import problem_doc


def build_router(catalog: Catalog) -> APIRouter:
    router = APIRouter(prefix="/api/v1", tags=["catalog"])

    @router.get(
        "/shops",
        response_model=ShopList,
        summary="Магазины",
        description=(
            "Магазины мерчантов. Каждый — отдельный тенант платформы со своими "
            "скриптами на хуке checkout.discount."
        ),
    )
    async def list_shops() -> ShopList:
        return ShopList(items=catalog.shops())

    @router.get(
        "/shops/{shop_id}/products",
        response_model=ProductList,
        summary="Каталог магазина",
        responses={404: problem_doc("Магазин не найден")},
    )
    async def list_products(shop_id: str) -> ProductList:
        return ProductList(items=catalog.products(shop_id))

    @router.get(
        "/customers",
        response_model=CustomerList,
        summary="Демо-покупатели",
        description=(
            "Учётных записей нет: витрина даёт выбрать, кто покупает. От суммы "
            "прошлых покупок зависит скидка скрипта мерчанта."
        ),
    )
    async def list_customers() -> CustomerList:
        return CustomerList(items=catalog.customers())

    return router
