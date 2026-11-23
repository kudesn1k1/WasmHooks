"use client";

import { useQuery } from "@tanstack/react-query";
import { Minus, Plus, Trash2 } from "lucide-react";
import Link from "next/link";
import { useState } from "react";

import { customersQuery, MAX_CART_QUANTITY, type Checkout } from "@/entities/shop";
import { useShopCart } from "@/features/shop-cart";
import { useCheckout } from "@/features/shop-checkout";
import { describeError } from "@/shared/api";
import { routes } from "@/shared/config/routes";
import { formatPrice } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { EmptyState, ErrorState, LoadingRows } from "@/shared/ui/query-state";

type Cart = NonNullable<ReturnType<typeof useShopCart>["cart"]>;

function CheckoutSummary({ result }: { result: Checkout }) {
  return (
    <div role="status" className="space-y-3 rounded-md border p-4 text-sm">
      <dl className="space-y-2 tabular-nums">
        <div className="flex justify-between gap-4">
          <dt>Сумма без скидки</dt><dd>{formatPrice(result.subtotal)}</dd>
        </div>
        <div className="flex justify-between gap-4">
          <dt>Скидка ({result.discount.percent}%)</dt><dd>{formatPrice(result.discount.amount)}</dd>
        </div>
        <div className="flex justify-between gap-4 border-t pt-2 font-semibold">
          <dt>Итого</dt><dd>{formatPrice(result.total)}</dd>
        </div>
      </dl>
      {result.discount.fallback && (
        <p className="text-muted-foreground">
          Расчёт завершён без скидки. Исход хука: <code>{result.discount.outcome}</code>.
        </p>
      )}
      {result.discount.reason && <p className="text-muted-foreground">{result.discount.reason}</p>}
    </div>
  );
}

function CartContents({ cart }: { cart: Cart }) {
  const { clear, setQuantity } = useShopCart();
  const customers = useQuery(customersQuery);
  const checkout = useCheckout(cart.shop.id);
  const [selectedCustomer, setSelectedCustomer] = useState("");
  const customer = customers.data?.items.find((item) => item.id === selectedCustomer) ?? customers.data?.items[0];
  const subtotal = cart.lines.reduce((sum, { product, quantity }) => sum + product.price * quantity, 0);

  function changeQuantity(productId: string, quantity: number) {
    checkout.reset();
    setQuantity(productId, quantity);
  }

  return (
    <div className="grid items-start gap-4 lg:grid-cols-[1fr_22rem]">
      <Card>
        <CardHeader className="flex flex-wrap items-center justify-between gap-3">
          <CardTitle><h2>{cart.shop.name}</h2></CardTitle>
          <Button variant="outline" size="sm" disabled={checkout.isPending} onClick={clear}>
            <Trash2 />
            Очистить корзину
          </Button>
        </CardHeader>
        <CardContent>
          <ul className="divide-y">
            {cart.lines.map(({ product, quantity }) => (
              <li key={product.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
                <div>
                  <p className="font-medium">{product.name}</p>
                  <p className="text-muted-foreground text-sm tabular-nums">{formatPrice(product.price)} за шт.</p>
                </div>
                <div className="flex items-center gap-2">
                  <Button variant="outline" size="icon" aria-label={`Уменьшить количество: ${product.name}`} disabled={checkout.isPending || quantity === 1} onClick={() => changeQuantity(product.id, quantity - 1)}>
                    <Minus />
                  </Button>
                  <span className="w-6 text-center text-sm tabular-nums">{quantity}</span>
                  <Button variant="outline" size="icon" aria-label={`Увеличить количество: ${product.name}`} disabled={checkout.isPending || quantity === MAX_CART_QUANTITY} onClick={() => changeQuantity(product.id, quantity + 1)}>
                    <Plus />
                  </Button>
                  <Button variant="ghost" size="icon" aria-label={`Удалить: ${product.name}`} disabled={checkout.isPending} onClick={() => changeQuantity(product.id, 0)}>
                    <Trash2 />
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle><h2>Оформление</h2></CardTitle></CardHeader>
        <CardContent className="space-y-4">
          {customers.isPending && <LoadingRows rows={1} />}
          {customers.isError && <ErrorState error={customers.error} />}
          {customers.isSuccess && !customer && <EmptyState>Нет демо-покупателей.</EmptyState>}
          {customer && (
            <div className="space-y-2">
              <Label htmlFor="customer">Покупатель</Label>
              <NativeSelect
                id="customer"
                value={customer.id}
                disabled={checkout.isPending}
                onChange={(event) => {
                  checkout.reset();
                  setSelectedCustomer(event.target.value);
                }}
              >
                {customers.data?.items.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
              </NativeSelect>
              <p className="text-muted-foreground text-sm">Прошлые покупки: {formatPrice(customer.lifetime_spend)}</p>
            </div>
          )}
          {!checkout.data && (
            <p className="flex justify-between gap-4 text-sm">
              <span>Сумма корзины</span><strong className="tabular-nums">{formatPrice(subtotal)}</strong>
            </p>
          )}
          <Button
            className="w-full"
            disabled={!customer || checkout.isPending}
            onClick={() => customer && checkout.mutate({
              customer_id: customer.id,
              items: cart.lines.map(({ product, quantity }) => ({ product_id: product.id, quantity })),
            })}
          >
            {checkout.isPending ? "Рассчитываем…" : "Рассчитать заказ"}
          </Button>
          {checkout.isError && <p role="alert" className="text-destructive text-sm">{describeError(checkout.error)}</p>}
          {checkout.data && <CheckoutSummary result={checkout.data} />}
        </CardContent>
      </Card>
    </div>
  );
}

export function StorefrontCartPage() {
  const { cart, isRestoring } = useShopCart();

  return (
    <section className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">Корзина</h1>
        <Button asChild variant="outline"><Link href={routes.storefront.catalog}>К каталогу</Link></Button>
      </div>
      {isRestoring && <LoadingRows rows={1} />}
      {cart && <CartContents key={cart.shop.id} cart={cart} />}
      {!isRestoring && !cart && <EmptyState>Корзина пуста.</EmptyState>}
    </section>
  );
}
