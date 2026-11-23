"use client";

import Link from "next/link";

import { useShopCart } from "@/features/shop-cart";
import { routes } from "@/shared/config/routes";
import { Button } from "@/shared/ui/button";
import { EmptyState, LoadingRows } from "@/shared/ui/query-state";
import { StorefrontCart } from "@/widgets/storefront-cart";

export function StorefrontCartPage() {
  const { cart, isRestoring } = useShopCart();

  return (
    <section className="mx-auto max-w-lg space-y-6" aria-label="Корзина">
      {isRestoring && <LoadingRows rows={1} />}
      {cart && (
        <>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <h1 className="text-2xl font-semibold tracking-tight">{cart.shop.name}</h1>
            <Button asChild variant="outline"><Link href={routes.storefront.shop(cart.shop.id)}>К товарам</Link></Button>
          </div>
          <StorefrontCart key={cart.shop.id} shopId={cart.shop.id} />
        </>
      )}
      {!isRestoring && !cart && (
        <>
          <h1 className="text-2xl font-semibold tracking-tight">Корзина</h1>
          <EmptyState>Корзина пуста.</EmptyState>
        </>
      )}
    </section>
  );
}
