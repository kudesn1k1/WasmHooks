export type ConsoleKind = "operator" | "tenant";

const storageKeys: Record<ConsoleKind, string> = {
  operator: "wasmhooks.operator.key",
  tenant: "wasmhooks.tenant.token",
};

const listeners = new Set<() => void>();

function storage(): Storage | null {
  try {
    return typeof window === "undefined" ? null : window.sessionStorage;
  } catch {
    return null;
  }
}

export function consoleForPath(path: string): ConsoleKind {
  return path === "/tenant" || path.startsWith("/tenant/") ? "tenant" : "operator";
}

export function getToken(kind: ConsoleKind): string | null {
  return storage()?.getItem(storageKeys[kind]) || null;
}

export function setToken(kind: ConsoleKind, token: string) {
  storage()?.setItem(storageKeys[kind], token);
  listeners.forEach((notify) => notify());
}

export function clearToken(kind: ConsoleKind) {
  storage()?.removeItem(storageKeys[kind]);
  listeners.forEach((notify) => notify());
}

export function subscribeTokens(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
