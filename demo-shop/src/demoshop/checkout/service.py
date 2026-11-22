import logging

from pydantic import BaseModel, Field, ValidationError

from demoshop.catalog.schemas import Customer, Shop
from demoshop.catalog.service import Catalog
from demoshop.checkout.schemas import (
    Checkout,
    CheckoutLine,
    CheckoutRequest,
    Discount,
    DiscountOutcome,
)
from demoshop.invoke.client import InvokeClient, InvokeFailed, Outcome

logger = logging.getLogger(__name__)


class DiscountResult(BaseModel):
    """The hook's output the shop relies on (output_schema of checkout.discount)."""

    discount_percent: int = Field(ge=0, le=100)
    reason: str


class CheckoutService:
    def __init__(
        self, catalog: Catalog, invoke: InvokeClient, *, hook: str, deadline_ms: int
    ) -> None:
        self._catalog = catalog
        self._invoke = invoke
        self._hook = hook
        self._deadline_ms = deadline_ms

    async def checkout(self, shop_id: str, request: CheckoutRequest) -> Checkout:
        shop = self._catalog.shop(shop_id)
        customer = self._catalog.customer(request.customer_id)
        lines = [self._line(shop, item.product_id, item.quantity) for item in request.items]
        subtotal = sum(line.line_total for line in lines)
        discount = await self._discount(shop, customer, subtotal)
        return Checkout(
            shop_id=shop.id,
            customer_id=customer.id,
            lines=lines,
            subtotal=subtotal,
            discount=discount,
            total=subtotal - discount.amount,
        )

    def _line(self, shop: Shop, product_id: str, quantity: int) -> CheckoutLine:
        product = self._catalog.product(shop.id, product_id)
        return CheckoutLine(
            product_id=product.id,
            name=product.name,
            unit_price=product.price,
            quantity=quantity,
            line_total=product.price * quantity,
        )

    async def _discount(self, shop: Shop, customer: Customer, subtotal: int) -> Discount:
        """Asks the shop's merchant script for a discount. Whatever goes wrong,
        the checkout goes on with the default: no discount."""
        payload = {
            "cart_total": subtotal / 100,
            "customer": {"id": customer.id, "lifetime_spend": customer.lifetime_spend / 100},
        }
        try:
            response = await self._invoke.invoke(self._hook, shop.id, payload, self._deadline_ms)
        except InvokeFailed as exc:
            logger.error("discount hook failed for %s, no discount applied: %s", shop.id, exc)
            return _no_discount(DiscountOutcome.ERROR)
        outcome = DiscountOutcome(response.outcome.value)
        if response.outcome is not Outcome.OK:
            if response.outcome is not Outcome.NO_HANDLER:
                logger.warning(
                    "discount hook outcome %s for %s, no discount applied: %s",
                    response.outcome,
                    shop.id,
                    response.error,
                )
            return _no_discount(outcome)
        try:
            result = DiscountResult.model_validate(response.result)
        except ValidationError as exc:
            logger.error("discount hook result of %s does not fit the shop: %s", shop.id, exc)
            return _no_discount(outcome)
        return Discount(
            percent=result.discount_percent,
            amount=subtotal * result.discount_percent // 100,
            reason=result.reason,
            outcome=outcome,
            fallback=False,
        )


def _no_discount(outcome: DiscountOutcome) -> Discount:
    return Discount(percent=0, amount=0, reason=None, outcome=outcome, fallback=True)
