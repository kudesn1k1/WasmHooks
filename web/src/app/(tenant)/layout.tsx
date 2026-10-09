import type { ReactNode } from "react";

import { SessionGate } from "@/features/console-session";
import { routes } from "@/shared/config/routes";
import { ConsoleShell, type ConsoleNavItem } from "@/widgets/console-shell";

const nav: ConsoleNavItem[] = [
  { href: routes.tenant.hooks, label: "Хуки" },
  { href: routes.tenant.invocations, label: "Лог вызовов" },
];

export default function TenantLayout({ children }: { children: ReactNode }) {
  return (
    <ConsoleShell title="Консоль тенанта" nav={nav}>
      <SessionGate
        kind="tenant"
        title="Сессия консоли не начата или истекла"
        description="Откройте консоль заново из магазина или вставьте токен консоли тенанта."
        label="Токен консоли тенанта"
      >
        {children}
      </SessionGate>
    </ConsoleShell>
  );
}
