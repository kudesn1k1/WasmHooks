"use client";

import { useSessionToken } from "@/shared/api";
import { formatDateTime } from "@/shared/lib/format";
import { readJwtClaims } from "@/shared/lib/jwt";

import { MOCK_TENANT_TOKEN } from "../model/tenant-token";

export function TenantSessionInfo() {
  const token = useSessionToken("tenant");
  if (!token) return null;

  const claims = readJwtClaims(token);
  const tenant = claims?.sub ?? (token === MOCK_TENANT_TOKEN ? "мок-сессия" : "не указан в токене");

  return (
    <dl className="mb-3 flex flex-col gap-1 text-xs">
      <div>
        <dt className="text-muted-foreground">Тенант</dt>
        <dd className="font-mono break-all">{tenant}</dd>
      </div>
      {claims?.exp != null && (
        <div>
          <dt className="text-muted-foreground">Сессия до</dt>
          <dd>{formatDateTime(new Date(claims.exp * 1000).toISOString())}</dd>
        </div>
      )}
    </dl>
  );
}
