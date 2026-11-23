"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { MAX_CART_ITEMS, MAX_CART_QUANTITY, productsQuery, ShopArt, shopsQuery, shopTone, type Shop } from "@/entities/shop";
import { OpenShopConsoleButton } from "@/features/open-tenant-console";
import { useShopCart } from "@/features/shop-cart";
import { describeError } from "@/shared/api";
import { routes } from "@/shared/config/routes";
import { formatPrice } from "@/shared/lib/format";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { EmptyState, LoadingRows } from "@/shared/ui/query-state";
import { RequestError } from "@/shared/ui/request-error";
import { StorefrontCart } from "@/widgets/storefront-cart";

function ShopProducts({ shop }: { shop: Shop }) {
  const products = useQuery(productsQuery(shop.id));
  const { cart, isRestoring, add } = useShopCart();
  const otherShop = cart && cart.shop.id !== shop.id;

  if (products.isPending) return <LoadingRows />;
  if (products.isError) {
    return (
      <div className="space-y-3">
        <RequestError title="Не удалось загрузить товары">{describeError(products.error)}</RequestError>
        <Button variant="outline" onClick={() => void products.refetch()}>Повторить загрузку</Button>
      </div>
    );
  }
  if (!products.data.items.length) return <EmptyState>В этом магазине пока нет товаров.</EmptyState>;

  return (
    <ul className="space-y-4">
      {products.data.items.map((product) => {
        const quantity = otherShop ? 0 : cart?.lines.find((line) => line.product.id === product.id)?.quantity ?? 0;
        const limitReached = quantity >= MAX_CART_QUANTITY || (quantity === 0 && cart?.lines.length === MAX_CART_ITEMS);

        return (
          <li key={product.id}>
            <Card className="flex-row flex-wrap items-center gap-4 p-4 shadow-none sm:flex-nowrap sm:gap-5 sm:p-6">
              <ShopArt id={product.id} className="size-16 sm:size-20" />
              <div className="min-w-0 flex-1 space-y-2">
                <h3 className="font-semibold">{product.name}</h3>
                <p className="text-muted-foreground text-sm">{product.description}</p>
              </div>
              <div className="ml-auto flex items-center justify-end gap-3 sm:flex-col sm:items-end">
                <p className="text-lg font-semibold tabular-nums">{formatPrice(product.price)}</p>
                <Button
                  variant="outline"
                  className="min-w-28 sm:min-w-36"
                  aria-label={`В корзину: ${product.name}`}
                  disabled={isRestoring || Boolean(otherShop) || limitReached}
                  onClick={() => add(shop, product)}
                >
                  В корзину
                </Button>
              </div>
            </Card>
          </li>
        );
      })}
    </ul>
  );
}

export function StorefrontShopPage({ shopId }: { shopId: string }) {
  const shops = useQuery(shopsQuery);
  if (shops.isPending) return <LoadingRows />;
  if (shops.isError) {
    return (
      <div className="space-y-3">
        <RequestError title="Не удалось загрузить магазин">{describeError(shops.error)}</RequestError>
        <Button variant="outline" onClick={() => void shops.refetch()}>Повторить загрузку</Button>
      </div>
    );
  }

  const shop = shops.data.items.find((item) => item.id === shopId);
  if (!shop) {
    return (
      <EmptyState>
        <p>Магазин не найден.</p>
        <Link className="underline" href={routes.storefront.catalog}>Все магазины</Link>
      </EmptyState>
    );
  }

  return (
    <section className="space-y-6">
      <header className={cn("flex flex-wrap items-center gap-4 rounded-xl px-4 py-4 sm:px-6", shopTone(shop.id))}>
        <ShopArt id={shop.id} className="size-12 bg-transparent dark:bg-transparent" />
        <div className="min-w-0 flex-1">
          <h1 className="text-foreground text-2xl font-semibold tracking-tight sm:text-3xl">{shop.name}</h1>
          <p className="text-muted-foreground mt-1 text-sm">{shop.description}</p>
        </div>
        <OpenShopConsoleButton key={shop.id} shopId={shop.id} />
      </header>
      <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,25fr)_minmax(0,13fr)] lg:gap-8">
        <section aria-labelledby="shop-products" className="min-w-0 space-y-4">
          <h2 id="shop-products" className="text-lg font-semibold">Товары</h2>
          <ShopProducts key={shop.id} shop={shop} />
        </section>
        <StorefrontCart key={shop.id} shopId={shop.id} />
      </div>
    </section>
  );
}
