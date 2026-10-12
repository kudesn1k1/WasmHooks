import { queryOptions } from "@tanstack/react-query";

import { apiRequest } from "@/shared/api";

import { operatorHookListSchema, operatorHookSchema } from "../model/operator-schema";

export const operatorHookKeys = {
  all: ["operator", "hooks"] as const,
  list: () => [...operatorHookKeys.all, "list"] as const,
  detail: (name: string) => [...operatorHookKeys.all, "detail", name] as const,
};

export const operatorHookQueries = {
  list: () =>
    queryOptions({
      queryKey: operatorHookKeys.list(),
      queryFn: ({ signal }) =>
        apiRequest("/hooks", { schema: operatorHookListSchema, signal }).then((r) => r.items),
    }),
  detail: (name: string) =>
    queryOptions({
      queryKey: operatorHookKeys.detail(name),
      queryFn: ({ signal }) =>
        apiRequest(`/hooks/${encodeURIComponent(name)}`, { schema: operatorHookSchema, signal }),
    }),
};
