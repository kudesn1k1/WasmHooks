import { ShoppingCart } from "lucide-react";
import Link from "next/link";

import { routes } from "@/shared/config/routes";
import { Button } from "@/shared/ui/button";

export function StorefrontHeader() {
  return (
    <header className="border-b">
      <div className="mx-auto flex h-14 max-w-5xl items-center justify-between px-4">
        <Link href={routes.storefront.catalog} className="font-semibold">
          Демо-магазин
        </Link>
        <Button asChild variant="ghost" size="sm">
          <Link href={routes.storefront.cart}>
            <ShoppingCart />
            Корзина
          </Link>
        </Button>
      </div>
    </header>
  );
}
