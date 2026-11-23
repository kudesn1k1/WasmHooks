"use client";

import { useQuery } from "@tanstack/react-query";
import { Info, Minus, Plus } from "lucide-react";
import Link from "next/link";
import { useId, useState } from "react";

import { customersQuery, MAX_CART_QUANTITY, type Checkout, type ShopCart } from "@/entities/shop";
import { useShopCart } from "@/features/shop-cart";
import { useCheckout } from "@/features/shop-checkout";
import { describeError } from "@/shared/api";
import { routes } from "@/shared/config/routes";
import { formatPrice } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader } from "@/shared/ui/card";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { LoadingRows } from "@/shared/ui/query-state";
import { RequestError } from "@/shared/ui/request-error";

const itemCount = new Intl.PluralRules("ru");
const itemLabels = { one: "товар", few: "товара", many: "товаров", other: "товара", zero: "товаров", two: "товара" };

function DiscountNote({ discount }: { discount: Checkout["discount"] }) {
  if (discount.fallback) {
    return (
      <div className="bg-muted/60 flex items-start gap-2 rounded-md border p-3 text-sm">
        <Info aria-hidden="true" className="text-muted-foreground mt-0.5 size-4 shrink-0" />
        <div className="space-y-1">
          <p className="font-medium">Расчёт без скидки</p>
          <p className="text-muted-foreground text-xs">
            {discount.outcome === "no_handler"
              ? "Магазин пока не подключил скидки. Итог рассчитан по ценам каталога."
              : <>Итог рассчитан по ценам каталога. Исход хука: <code>{discount.outcome}</code>.</>}
          </p>
        </div>
      </div>
    );
  }

  if (discount.percent === 0) {
    return <p className="text-muted-foreground text-sm">Для этого покупателя скидка не предусмотрена.</p>;
  }

  const reason = discount.reason === "loyal_customer" ? "Скидка постоянного покупателя" : discount.reason;
  return reason && <p className="text-sm text-green-700 wrap-anywhere dark:text-green-400">{reason}</p>;
}

function Totals({ subtotal, result }: { subtotal: number; result: Checkout | undefined }) {
  return (
    <div role={result ? "status" : undefined} className="space-y-3">
      <dl className="space-y-2 text-sm tabular-nums">
        <div className="flex justify-between gap-4">
          <dt className="text-muted-foreground">Товары</dt>
          <dd className="font-medium">{formatPrice(result?.subtotal ?? subtotal)}</dd>
        </div>
        <div className="flex justify-between gap-4">
          <dt className="text-muted-foreground">Скидка{result?.discount.percent ? ` ${result.discount.percent}%` : ""}</dt>
          <dd className={result?.discount.amount ? "font-medium text-green-700 dark:text-green-400" : "font-medium"}>
            {result ? formatPrice(-result.discount.amount || 0) : "После расчёта"}
          </dd>
        </div>
        {result && (
          <div className="bg-muted/70 flex items-center justify-between gap-4 rounded-md px-3 py-2.5 font-semibold">
            <dt>Итого</dt>
            <dd className="text-2xl">{formatPrice(result.total)}</dd>
          </div>
        )}
      </dl>
      {result && <DiscountNote discount={result.discount} />}
    </div>
  );
}

export function StorefrontCart({ shopId }: { shopId: string }) {
  const { cart, isRestoring, clear, setQuantity } = useShopCart();
  const currentCart = cart?.shop.id === shopId ? cart : null;
  const otherShop = cart && cart.shop.id !== shopId ? cart.shop : null;
  const customers = useQuery(customersQuery);
  const checkout = useCheckout(shopId);
  const customerSelectId = useId();
  const [selectedCustomer, setSelectedCustomer] = useState("");
  const [submittedCart, setSubmittedCart] = useState<ShopCart | null>(null);
  const customer = customers.data?.items.find((item) => item.id === selectedCustomer) ?? customers.data?.items[0];
  const isCurrentCalculation = Boolean(currentCart && submittedCart === currentCart && checkout.variables?.customer_id === customer?.id);
  const result = isCurrentCalculation ? checkout.data : undefined;
  const quantity = currentCart?.lines.reduce((sum, line) => sum + line.quantity, 0) ?? 0;
  const subtotal = currentCart?.lines.reduce((sum, line) => sum + line.product.price * line.quantity, 0) ?? 0;
  const busy = checkout.isPending;
  const calculateLabel = busy ? "Рассчитываем…" : result ? "Пересчитать" : isCurrentCalculation && checkout.isError ? "Повторить расчёт" : "Рассчитать итог";

  function calculate() {
    if (!currentCart || !customer) return;
    setSubmittedCart(currentCart);
    checkout.mutate({
      customer_id: customer.id,
      items: currentCart.lines.map(({ product, quantity }) => ({ product_id: product.id, quantity })),
    });
  }

  return (
    <Card className="min-w-0 gap-4 py-5 sm:py-6">
      <CardHeader className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Корзина</h2>
        <p className="text-muted-foreground text-xs" aria-live="polite">
          {quantity ? `${quantity} ${itemLabels[itemCount.select(quantity)]}` : "Пока пусто"}
        </p>
      </CardHeader>
      <CardContent className="space-y-4">
        {customers.isPending && <LoadingRows rows={1} />}
        {customers.isError && (
          <div className="space-y-2">
            <RequestError title="Не удалось загрузить покупателей">{describeError(customers.error)}</RequestError>
            <Button variant="outline" size="sm" onClick={() => void customers.refetch()}>Повторить загрузку</Button>
          </div>
        )}
        {customers.isSuccess && !customer && <p className="text-muted-foreground text-sm">Нет демо-покупателей.</p>}
        {customer && (
          <div className="space-y-2">
            <Label htmlFor={customerSelectId}>Демо-покупатель</Label>
            <NativeSelect
              id={customerSelectId}
              value={customer.id}
              disabled={busy}
              onChange={(event) => {
                checkout.reset();
                setSelectedCustomer(event.target.value);
              }}
            >
              {customers.data?.items.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
            </NativeSelect>
            <p className="text-muted-foreground text-xs">Прошлые покупки: {formatPrice(customer.lifetime_spend)}</p>
          </div>
        )}

        {isRestoring && <LoadingRows rows={1} />}
        {otherShop && (
          <div className="space-y-3 border-t pt-4 text-sm">
            <p role="status">В корзине уже есть товары магазина «{otherShop.name}».</p>
            <div className="flex flex-wrap gap-2">
              <Button asChild variant="outline" size="sm"><Link href={routes.storefront.shop(otherShop.id)}>Открыть корзину</Link></Button>
              <Button variant="ghost" size="sm" onClick={clear}>Очистить корзину</Button>
            </div>
          </div>
        )}
        {!isRestoring && !cart && (
          <div className="space-y-2 border-t pt-4">
            <p className="text-sm font-medium">Добавьте первый товар</p>
            <p className="text-muted-foreground text-sm">Товары появятся здесь. Затем можно будет рассчитать итог со скидкой.</p>
          </div>
        )}
        {currentCart && (
          <>
            <ul className="divide-y border-y">
              {currentCart.lines.map(({ product, quantity }) => (
                <li key={product.id} className="space-y-2 py-3 text-sm">
                  <div className="flex justify-between gap-3 font-medium">
                    <p>{product.name}</p>
                    <p className="shrink-0 tabular-nums">{formatPrice(product.price * quantity)}</p>
                  </div>
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                      <Button variant="outline" size="icon" className="size-7" aria-label={`Уменьшить количество: ${product.name}`} disabled={busy || quantity === 1} onClick={() => setQuantity(product.id, quantity - 1)}>
                        <Minus />
                      </Button>
                      <span className="w-5 text-center tabular-nums">{quantity}</span>
                      <Button variant="outline" size="icon" className="size-7" aria-label={`Увеличить количество: ${product.name}`} disabled={busy || quantity >= MAX_CART_QUANTITY} onClick={() => setQuantity(product.id, quantity + 1)}>
                        <Plus />
                      </Button>
                      <span className="text-muted-foreground text-xs tabular-nums">{formatPrice(product.price)} / шт.</span>
                    </div>
                    <Button variant="ghost" size="sm" className="h-7 px-2" aria-label={`Удалить: ${product.name}`} disabled={busy} onClick={() => setQuantity(product.id, 0)}>
                      Удалить
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
            <Totals subtotal={subtotal} result={result} />
            {isCurrentCalculation && checkout.isError && (
              <RequestError title="Не удалось рассчитать итог">{describeError(checkout.error)}. Корзина сохранена.</RequestError>
            )}
          </>
        )}
        <Button className="w-full" disabled={!currentCart || !customer || busy} onClick={calculate}>
          {calculateLabel}
        </Button>
        {currentCart && (
          <Button className="w-full" variant="ghost" size="sm" disabled={busy} onClick={clear}>Очистить корзину</Button>
        )}
        <p className="text-muted-foreground text-sm">Только расчёт, без оплаты.</p>
      </CardContent>
    </Card>
  );
}
