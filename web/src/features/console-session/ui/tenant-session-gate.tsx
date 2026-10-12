"use client";

import { useEffect, useState, type ReactNode } from "react";

import { clearToken, getToken, useSessionToken } from "@/shared/api";
import { API_MOCKING } from "@/shared/config/env";
import { isJwtExpired, readJwtClaims } from "@/shared/lib/jwt";

import {
  adoptTokenFromFragment,
  MAX_TIMER_DELAY_MS,
  MOCK_TENANT_TOKEN,
  verifyTenantToken,
} from "../model/tenant-token";
import { SessionGate } from "./session-gate";

const CONSOLE_SESSION_CURL =
  'curl -s -X POST localhost:8000/api/v1/tenants/merchant-a/console-sessions -H "Authorization: Bearer $KEY"';

function DevHint() {
  return (
    <div className="bg-muted/50 flex flex-col gap-2 rounded-md border p-3 text-xs">
      <p className="font-medium">Подсказка для разработки</p>
      {API_MOCKING === "all" && (
        <p>
          Моки включены: <a href={`#token=${MOCK_TENANT_TOKEN}`} className="font-mono underline">/tenant#token={MOCK_TENANT_TOKEN}</a>
        </p>
      )}
      <p>Токен от настоящего control plane выдаёт оператор ключом установки:</p>
      <pre className="bg-background overflow-x-auto rounded p-2 font-mono whitespace-pre-wrap break-all">
        {CONSOLE_SESSION_CURL}
      </pre>
      <p>
        Затем откройте <span className="font-mono">/tenant#token=&lt;token&gt;</span> или вставьте токен в поле выше.
      </p>
    </div>
  );
}

export function TenantSessionGate({
  children,
  devHint = process.env.NODE_ENV === "development",
}: {
  children: ReactNode;
  devHint?: boolean;
}) {
  const token = useSessionToken("tenant");
  const [ready, setReady] = useState(false);

  useEffect(() => {
    adoptTokenFromFragment();
    const current = getToken("tenant");
    if (current && isJwtExpired(current, Date.now())) clearToken("tenant");
    setReady(true);
    window.addEventListener("hashchange", adoptTokenFromFragment);
    return () => window.removeEventListener("hashchange", adoptTokenFromFragment);
  }, []);

  useEffect(() => {
    const exp = token ? readJwtClaims(token)?.exp : null;
    if (exp == null) return;
    const expiresAt = exp * 1000;
    let timer: ReturnType<typeof setTimeout> | undefined;
    function check() {
      const left = expiresAt - Date.now();
      if (left <= 0) clearToken("tenant");
      else timer = setTimeout(check, Math.min(left, MAX_TIMER_DELAY_MS));
    }
    check();
    return () => clearTimeout(timer);
  }, [token]);

  if (!ready) return null;

  return (
    <SessionGate
      kind="tenant"
      title="Сессия консоли не начата или истекла"
      description="Откройте консоль заново из магазина."
      label="Токен консоли тенанта"
      verify={verifyTenantToken}
      hint={devHint ? <DevHint /> : undefined}
    >
      {children}
    </SessionGate>
  );
}
