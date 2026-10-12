"use client";

import { useSyncExternalStore } from "react";

import { getToken, subscribeTokens, type ConsoleKind } from "./session";

export function useSessionToken(kind: ConsoleKind): string | null | undefined {
  return useSyncExternalStore(
    subscribeTokens,
    () => getToken(kind),
    () => undefined,
  );
}
