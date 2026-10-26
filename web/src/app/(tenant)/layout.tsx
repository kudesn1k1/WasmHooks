import type { ReactNode } from "react";

import { SignOutButton, TenantName, TenantSessionGate, TenantSessionInfo } from "@/features/console-session";
import { routes } from "@/shared/config/routes";
import { ConsoleShell, type ConsoleNavItem } from "@/widgets/console-shell";

const nav: ConsoleNavItem[] = [{ href: routes.tenant.hooks, label: "Хуки", hint: "Управление обработчиками" }];

export default function TenantLayout({ children }: { children: ReactNode }) {
  return (
    <ConsoleShell
      title="Консоль тенанта"
      context={<TenantName />}
      nav={nav}
      footer={
        <>
          <TenantSessionInfo />
          <SignOutButton kind="tenant" />
        </>
      }
    >
      <TenantSessionGate>{children}</TenantSessionGate>
    </ConsoleShell>
  );
}
