import { queryOptions } from "@tanstack/react-query";

import { apiRequest } from "@/shared/api";

import { invocationListSchema, type Outcome } from "../model/schema";

export type InvocationFilter = {
  outcome?: Outcome;
  hook?: string;
};

export const invocationKeys = {
  all: ["tenant", "invocations"] as const,
  list: (filter: InvocationFilter) => [...invocationKeys.all, filter] as const,
};

export const INVOCATIONS_POLL_MS = 5_000;

export const invocationQueries = {
  list: (filter: InvocationFilter) =>
    queryOptions({
      queryKey: invocationKeys.list(filter),
      queryFn: ({ signal }) =>
        apiRequest("/tenant/invocations", {
          schema: invocationListSchema,
          query: { outcome: filter.outcome, hook: filter.hook, limit: 100 },
          signal,
        }).then((r) => r.items),
      refetchInterval: INVOCATIONS_POLL_MS,
    }),
};
