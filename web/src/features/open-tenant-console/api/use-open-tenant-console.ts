import { useMutation } from "@tanstack/react-query";

import { consoleSessionSchema } from "@/entities/tenant";
import { apiRequest } from "@/shared/api";
import { routes } from "@/shared/config/routes";

export class PopupBlockedError extends Error {
  constructor() {
    super("Браузер не дал открыть новую вкладку. Разрешите всплывающие окна для этого сайта.");
    this.name = "PopupBlockedError";
  }
}

function tenantConsoleUrl(token: string): string {
  return `${window.location.origin}${routes.tenant.root}#token=${encodeURIComponent(token)}`;
}

function detach(tab: Window) {
  tab.opener = null;
  try {
    tab.sessionStorage.clear();
  } catch {
    return;
  }
}

export function useOpenTenantConsole(externalId: string) {
  return useMutation({
    mutationFn: async (tab: Window | null) => {
      if (!tab) throw new PopupBlockedError();
      try {
        detach(tab);
        const session = await apiRequest(`/tenants/${encodeURIComponent(externalId)}/console-sessions`, {
          method: "POST",
          schema: consoleSessionSchema,
        });
        tab.location.href = tenantConsoleUrl(session.token);
        return session;
      } catch (error) {
        tab.close();
        throw error;
      }
    },
  });
}
