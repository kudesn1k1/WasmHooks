"use client";

import { formatDateTime } from "@/shared/lib/format";

import { useTenantSession } from "../model/use-tenant-session";

export function TenantName() {
  const session = useTenantSession();
  if (!session) return null;
  return <span>{session.tenant ?? "тенант не указан в токене"}</span>;
}

export function TenantSessionInfo() {
  const session = useTenantSession();
  if (!session?.expiresAt) return null;

  return (
    <dl className="flex flex-col gap-1 text-xs">
      <dt className="text-muted-foreground">Сессия до</dt>
      <dd>{formatDateTime(session.expiresAt.toISOString())}</dd>
    </dl>
  );
}
