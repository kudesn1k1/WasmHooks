"use client";

import { useSessionToken } from "@/shared/api";
import { readJwtClaims } from "@/shared/lib/jwt";

import { MOCK_TENANT, MOCK_TENANT_TOKEN } from "./tenant-token";

export type TenantSession = { tenant: string | null; expiresAt: Date | null };

export function useTenantSession(): TenantSession | null {
  const token = useSessionToken("tenant");
  if (!token) return null;
  if (token === MOCK_TENANT_TOKEN) return { tenant: MOCK_TENANT, expiresAt: null };
  const claims = readJwtClaims(token);
  return {
    tenant: claims?.sub ?? null,
    expiresAt: claims?.exp != null ? new Date(claims.exp * 1000) : null,
  };
}
