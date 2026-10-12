import type { RequestHandler } from "msw";

import { handlerGroups, type HandlerGroup } from "./handlers";

export const pendingGroups: Partial<Record<HandlerGroup, string>> = {
  tenantHooks: "T6",
  tenantModules: "T3, T4",
  tenantBinding: "T5",
  invocations: "MVP",
};

export const pendingHandlers: RequestHandler[] = (Object.keys(pendingGroups) as HandlerGroup[]).flatMap(
  (group) => handlerGroups[group],
);
