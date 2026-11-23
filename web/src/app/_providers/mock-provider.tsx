"use client";

import { useEffect, useState, type ReactNode } from "react";

import type { MockingMode } from "@/shared/config/env";

let started: Promise<unknown> | undefined;

function startWorker(mode: Exclude<MockingMode, "disabled">) {
  started ??= import("@/mocks/browser").then(({ createWorker }) =>
    createWorker(mode).start({ onUnhandledRequest: "bypass" }),
  );
  return started;
}

type State = "starting" | "ready" | "failed";

export function MockProvider({ children, mode }: { children: ReactNode; mode: MockingMode }) {
  const [state, setState] = useState<State>(mode === "disabled" ? "ready" : "starting");

  useEffect(() => {
    if (mode === "disabled") return;
    startWorker(mode).then(
      () => setState("ready"),
      (error: unknown) => {
        console.error("[MSW] не удалось запустить моки API", error);
        setState("failed");
      },
    );
  }, [mode]);

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
        </div>
      )}
      {children}
    </>
  );
}
