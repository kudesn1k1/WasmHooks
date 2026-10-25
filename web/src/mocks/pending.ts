import type { RequestHandler } from "msw";

import { handlerGroups, type HandlerGroup } from "./handlers";

export const pendingGroups: Partial<Record<HandlerGroup, string>> = {
  invocations: "MVP",
};

export const pendingHandlers: RequestHandler[] = (Object.keys(pendingGroups) as HandlerGroup[]).flatMap(
  (group) => handlerGroups[group],
);
