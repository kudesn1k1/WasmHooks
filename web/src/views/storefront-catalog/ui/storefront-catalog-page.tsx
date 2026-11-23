"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { ShopArt, shopsQuery } from "@/entities/shop";
import { describeError } from "@/shared/api";
import { routes } from "@/shared/config/routes";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { EmptyState, LoadingRows } from "@/shared/ui/query-state";
import { RequestError } from "@/shared/ui/request-error";

export function StorefrontCatalogPage() {
  const shops = useQuery(shopsQuery);

  return (
    <section className="space-y-6">
      <div className="space-y-3">
        <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">Магазины</h1>
        <p className="text-muted-foreground text-sm sm:text-base">Выберите магазин, добавьте товары в корзину и посмотрите, как меняется скидка.</p>
      </div>
      {shops.isPending && <LoadingRows rows={2} />}
      {shops.isError && (
        <div className="space-y-3">
          <RequestError title="Не удалось загрузить магазины">{describeError(shops.error)}</RequestError>
          <Button variant="outline" onClick={() => void shops.refetch()}>Повторить загрузку</Button>
        </div>
      )}
      {shops.isSuccess && (
        shops.data.items.length ? (
          <ul className="grid gap-5 md:grid-cols-2 lg:gap-8">
            {shops.data.items.map((shop) => (
              <li key={shop.id}>
                <Card className="h-full flex-row items-center gap-4 p-4 shadow-none sm:gap-6 sm:p-6">
                  <ShopArt id={shop.id} className="h-32 w-20 sm:h-44 sm:w-36" />
                  <div className="min-w-0 space-y-4">
                    <div className="space-y-2">
                      <h2 className="text-xl font-semibold tracking-tight sm:text-2xl">{shop.name}</h2>
                      <p className="text-muted-foreground text-sm">{shop.description}</p>
                    </div>
                    <Button asChild className="sm:px-6">
                      <Link href={routes.storefront.shop(shop.id)} aria-label={`Открыть магазин «${shop.name}»`}>Открыть магазин</Link>
                    </Button>
                  </div>
                </Card>
              </li>
            ))}
          </ul>
        ) : <EmptyState>Пока нет магазинов.</EmptyState>
      )}
      <p className="text-muted-foreground text-sm">Учебная витрина. Здесь можно рассчитать корзину и увидеть скидку.</p>
    </section>
  );
}
