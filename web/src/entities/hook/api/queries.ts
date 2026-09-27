import { queryOptions } from "@tanstack/react-query";

import { apiRequest } from "@/shared/api";

import { tenantHookListSchema, tenantHookSchema } from "../model/schema";

export const hookKeys = {
  all: ["tenant", "hooks"] as const,
  list: () => [...hookKeys.all, "list"] as const,
  detail: (name: string) => [...hookKeys.all, "detail", name] as const,
};

export const hookQueries = {
  list: () =>
    queryOptions({
      queryKey: hookKeys.list(),
      queryFn: ({ signal }) =>
        apiRequest("/tenant/hooks", { schema: tenantHookListSchema, signal }).then((r) => r.items),
    }),
  detail: (name: string) =>
    queryOptions({
      queryKey: hookKeys.detail(name),
      queryFn: ({ signal }) =>
        apiRequest(`/tenant/hooks/${encodeURIComponent(name)}`, {
          schema: tenantHookSchema,
          signal,
        }),
    }),
};
