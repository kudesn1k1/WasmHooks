import type { ReactNode } from "react";

import { SessionGate } from "@/features/console-session";
import { routes } from "@/shared/config/routes";
import { ConsoleShell, type ConsoleNavItem } from "@/widgets/console-shell";

const nav: ConsoleNavItem[] = [
  { href: routes.operator.hooks, label: "Хуки", badge: "MVP" },
  { href: routes.operator.tenants, label: "Тенанты", badge: "MVP" },
];

export default function OperatorLayout({ children }: { children: ReactNode }) {
  return (
    <ConsoleShell title="Консоль оператора" nav={nav}>
      <SessionGate
        kind="operator"
        title="Вход в консоль оператора"
        description="Ключ установки даёт полный доступ к установке. Вводите его только на своём компьютере."
        label="Ключ установки"
      >
        {children}
      </SessionGate>
    </ConsoleShell>
  );
}
