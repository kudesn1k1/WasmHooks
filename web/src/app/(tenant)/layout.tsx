import type { ReactNode } from "react";

import { routes } from "@/shared/config/routes";
import { ConsoleShell, type ConsoleNavItem } from "@/widgets/console-shell";

const nav: ConsoleNavItem[] = [
  { href: routes.tenant.hooks, label: "Хуки" },
  { href: routes.tenant.invocations, label: "Лог вызовов" },
];

export default function TenantLayout({ children }: { children: ReactNode }) {
  return (
    <ConsoleShell title="Консоль тенанта" nav={nav}>
      {children}
    </ConsoleShell>
  );
}
