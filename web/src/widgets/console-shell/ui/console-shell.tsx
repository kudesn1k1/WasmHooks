"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import type { ReactNode } from "react";

import { cn } from "@/shared/lib/utils";
import { Badge } from "@/shared/ui/badge";

export type ConsoleNavItem = {
  href: string;
  label: string;
  /** Пометка рядом с пунктом, например веха, в которой экран появится. */
  badge?: string;
};

type ConsoleShellProps = {
  title: string;
  nav: ConsoleNavItem[];
  children: ReactNode;
};

function isActive(pathname: string, href: string) {
  return pathname === href || pathname.startsWith(`${href}/`);
}

/** Общая оболочка консолей оператора и тенанта: боковая навигация и контент. */
export function ConsoleShell({ title, nav, children }: ConsoleShellProps) {
  const pathname = usePathname();

  return (
    <div className="flex min-h-screen">
      <aside className="bg-muted/40 flex w-60 shrink-0 flex-col border-r">
        <div className="border-b px-5 py-4">
          <p className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
            WasmHooks
          </p>
          <p className="font-semibold">{title}</p>
        </div>
        <nav className="flex flex-col gap-1 p-3">
          {nav.map((item) => {
            const active = isActive(pathname, item.href);
            return (
              <Link
                key={item.href}
                href={item.href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "hover:bg-accent hover:text-accent-foreground flex items-center justify-between rounded-md px-3 py-2 text-sm transition-colors",
                  active && "bg-accent text-accent-foreground font-medium",
                )}
              >
                {item.label}
                {item.badge && <Badge variant="outline">{item.badge}</Badge>}
              </Link>
            );
          })}
        </nav>
      </aside>
      <main className="flex-1 px-8 py-6">{children}</main>
    </div>
  );
}
