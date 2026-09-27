"use client";

import { useEffect, useState, type ReactNode } from "react";

import { API_MOCKING } from "@/shared/config/env";

let started: Promise<unknown> | undefined;

function startWorker() {
  if (process.env.NODE_ENV !== "development") return Promise.resolve();
  started ??= import("@/mocks/browser").then(({ worker }) =>
    worker.start({ onUnhandledRequest: "bypass" }),
  );
  return started;
}

type State = "starting" | "ready" | "failed";

export function MockProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<State>(API_MOCKING ? "starting" : "ready");

  useEffect(() => {
    if (!API_MOCKING) return;
    startWorker().then(
      () => setState("ready"),
      (error: unknown) => {
        console.error("[MSW] не удалось запустить моки API", error);
        setState("failed");
      },
    );
  }, []);

  if (state === "starting") {
    return (
      <div className="text-muted-foreground flex min-h-screen items-center justify-center text-sm">
        Запуск моков API…
      </div>
    );
  }

  return (
    <>
      {state === "failed" && (
        <div role="alert" className="bg-destructive px-4 py-2 text-sm text-white">
          Моки API не запустились, запросы уходят на настоящий бэкенд. Подробности в консоли браузера.
          Откройте приложение через http://localhost:3000.
        </div>
      )}
      {children}
    </>
  );
}
