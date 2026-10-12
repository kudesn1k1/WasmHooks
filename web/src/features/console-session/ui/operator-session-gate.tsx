"use client";

import type { ReactNode } from "react";

import { operatorHookListSchema } from "@/entities/hook";
import { apiRequest } from "@/shared/api";

import { SessionGate } from "./session-gate";

export function OperatorSessionGate({ children }: { children: ReactNode }) {
  return (
    <SessionGate
      kind="operator"
      title="Вход в консоль оператора"
      description="Ключ установки даёт полный доступ к установке. Вводите его только на своём компьютере."
      label="Ключ установки"
      rejectedMessage="Ключ не подошёл"
      verify={(token) => apiRequest("/hooks", { schema: operatorHookListSchema, token })}
    >
      {children}
    </SessionGate>
  );
}
