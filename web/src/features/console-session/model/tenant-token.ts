import { tenantHookListSchema } from "@/entities/hook";
import { ApiError, apiRequest, setToken } from "@/shared/api";
import { isJwtExpired } from "@/shared/lib/jwt";

export const MOCK_TENANT_TOKEN = "mock";

export const MOCK_TENANT = "merchant-a";

export const MAX_TIMER_DELAY_MS = 2_147_483_647;

export function takeTokenFromFragment(): string | null {
  const fragment = window.location.hash.replace(/^#/, "");
  if (!fragment) return null;
  const params = new URLSearchParams(fragment);
  if (!params.has("token")) return null;
  const token = params.get("token")?.trim() ?? "";
  params.delete("token");
  const rest = params.toString();
  window.history.replaceState(
    window.history.state,
    "",
    `${window.location.pathname}${window.location.search}${rest ? `#${rest}` : ""}`,
  );
  return token || null;
}

export function adoptTokenFromFragment() {
  const token = takeTokenFromFragment();
  if (token) setToken("tenant", token);
}

export async function verifyTenantToken(token: string) {
  if (isJwtExpired(token, Date.now())) throw new Error("Срок действия токена истёк");
  try {
    await apiRequest("/tenant/hooks", { schema: tenantHookListSchema, token });
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) throw error;
  }
}
