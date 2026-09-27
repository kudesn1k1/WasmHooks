import { queryOptions } from "@tanstack/react-query";

import { apiRequest } from "@/shared/api";

import { isPending, moduleListSchema } from "../model/schema";

export const moduleKeys = {
  all: ["tenant", "modules"] as const,
  byHook: (hook: string) => [...moduleKeys.all, hook] as const,
};

const PENDING_POLL_MS = 2_000;

export const moduleQueries = {
  byHook: (hook: string) =>
    queryOptions({
      queryKey: moduleKeys.byHook(hook),
      queryFn: ({ signal }) =>
        apiRequest(`/tenant/hooks/${encodeURIComponent(hook)}/modules`, {
          schema: moduleListSchema,
          signal,
        }).then((r) => r.items),
      refetchInterval: (query) =>
        query.state.data?.some((m) => isPending(m.status)) ? PENDING_POLL_MS : false,
    }),
};
