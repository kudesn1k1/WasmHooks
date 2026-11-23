"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { routes } from "@/shared/config/routes";
import { Button } from "@/shared/ui/button";

export function StorefrontHeader() {
  const pathname = usePathname();

  return (
    <header className="bg-background border-b">
      <div className="mx-auto flex min-h-16 max-w-[83rem] items-center justify-between gap-3 px-4 py-3 sm:min-h-20 sm:px-6 lg:px-10">
        <Link href={routes.storefront.catalog} className="flex flex-wrap items-center gap-x-4 gap-y-1">
          <span className="text-lg font-bold tracking-tight sm:text-xl">WasmHooks</span>
          <span className="text-muted-foreground text-sm">Демо-магазин</span>
        </Link>
        {pathname !== routes.storefront.catalog && (
          <Button asChild variant="ghost"><Link href={routes.storefront.catalog}>Все магазины</Link></Button>
        )}
      </div>
    </header>
  );
}
