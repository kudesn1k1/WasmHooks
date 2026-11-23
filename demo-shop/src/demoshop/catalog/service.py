from collections.abc import Sequence

from demoshop.catalog.schemas import Customer, Product, Shop
from demoshop.errors import ProblemError


class Catalog:
    """Read-only lookups over the marketplace data; unknown ids are 404."""

    def __init__(
        self,
        shops: Sequence[Shop],
        products: Sequence[Product],
        customers: Sequence[Customer],
    ) -> None:
        self._shops = tuple(shops)
        self._products = tuple(products)
        self._customers = tuple(customers)

    def shops(self) -> list[Shop]:
        return list(self._shops)

    def shop(self, shop_id: str) -> Shop:
        for shop in self._shops:
            if shop.id == shop_id:
                return shop
        raise ProblemError(404, "Магазин не найден", shop_id)

    def products(self, shop_id: str) -> list[Product]:
        shop = self.shop(shop_id)
        return [p for p in self._products if p.shop_id == shop.id]

    def product(self, shop_id: str, product_id: str) -> Product:
        """A product of this shop; another shop's product does not exist here."""
        for product in self.products(shop_id):
            if product.id == product_id:
                return product
        raise ProblemError(404, "Товар не найден", product_id)

    def customers(self) -> list[Customer]:
        return list(self._customers)

    def customer(self, customer_id: str) -> Customer:
        for customer in self._customers:
            if customer.id == customer_id:
                return customer
        raise ProblemError(404, "Покупатель не найден", customer_id)
