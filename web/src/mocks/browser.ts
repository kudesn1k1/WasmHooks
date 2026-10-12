import { setupWorker } from "msw/browser";

import type { MockingMode } from "@/shared/config/env";

import { handlers } from "./handlers";
import { pendingHandlers } from "./pending";

export function createWorker(mode: Exclude<MockingMode, "disabled">) {
  return setupWorker(...(mode === "all" ? handlers : pendingHandlers));
}
