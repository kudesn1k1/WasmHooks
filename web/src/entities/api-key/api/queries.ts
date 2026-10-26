import { queryOptions } from "@tanstack/react-query";

import { apiRequest } from "@/shared/api";

import { apiKeyListSchema } from "../model/schema";

export const apiKeyKeys = {
  all: ["operator", "api-keys"] as const,
  list: () => [...apiKeyKeys.all, "list"] as const,
};

export const apiKeyQueries = {
  list: () =>
    queryOptions({
      queryKey: apiKeyKeys.list(),
      queryFn: ({ signal }) => apiRequest("/api-keys", { schema: apiKeyListSchema, signal }).then((r) => r.items),
    }),
};
