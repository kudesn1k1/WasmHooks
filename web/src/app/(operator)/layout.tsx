import type { ReactNode } from "react";

import { OperatorSessionGate, SignOutButton } from "@/features/console-session";
import { routes } from "@/shared/config/routes";
import { ConsoleShell, type ConsoleNavItem } from "@/widgets/console-shell";

const nav: ConsoleNavItem[] = [
  { href: routes.operator.hooks, label: "Хуки" },
  { href: routes.operator.tenants, label: "Тенанты" },
  { href: routes.operator.apiKeys, label: "Ключи установки" },
];

export default function OperatorLayout({ children }: { children: ReactNode }) {
  return (
    <ConsoleShell title="Консоль оператора" nav={nav} footer={<SignOutButton kind="operator" />}>
      <OperatorSessionGate>{children}</OperatorSessionGate>
    </ConsoleShell>
  );
}
