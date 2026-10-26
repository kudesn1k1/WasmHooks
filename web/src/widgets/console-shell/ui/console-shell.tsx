"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import type { ReactNode } from "react";

import { cn } from "@/shared/lib/utils";
import { Badge } from "@/shared/ui/badge";

export type ConsoleNavItem = {
  href: string;
  label: string;
  badge?: string;
  hint?: string;
};

type ConsoleShellProps = {
  title: string;
  context?: ReactNode;
  nav: ConsoleNavItem[];
  footer?: ReactNode;
  children: ReactNode;
};

function isActive(pathname: string, href: string) {
  return pathname === href || pathname.startsWith(`${href}/`);
}

export function ConsoleShell({ title, context, nav, footer, children }: ConsoleShellProps) {
  const pathname = usePathname();

  return (
    <div className="flex min-h-screen">
      <aside className="bg-muted flex w-55 shrink-0 flex-col px-6 py-8">
        <p className="text-xl font-semibold">WasmHooks</p>
        <p className="text-muted-foreground mt-6 text-[11px] font-medium tracking-wide uppercase">{title}</p>
        {context && <div className="mt-3 text-sm font-medium break-all">{context}</div>}
        <nav className="mt-5 flex flex-col gap-2">
          {nav.map((item) => {
            const active = isActive(pathname, item.href);
            return (
              <div key={item.href} className="flex flex-col gap-4">
                <Link
                  href={item.href}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "flex h-10 items-center justify-center gap-2 rounded-md border text-sm font-medium transition-colors",
                    active
                      ? "bg-background"
                      : "text-muted-foreground hover:bg-background hover:text-foreground border-transparent",
                  )}
                >
                  {item.label}
                  {item.badge && <Badge variant="outline">{item.badge}</Badge>}
                </Link>
                {item.hint && <p className="text-muted-foreground text-xs">{item.hint}</p>}
              </div>
            );
          })}
        </nav>
        {footer && <div className="mt-auto flex flex-col gap-3 pt-6">{footer}</div>}
      </aside>
      <main className="min-w-0 flex-1 px-10 py-10">{children}</main>
    </div>
  );
}
