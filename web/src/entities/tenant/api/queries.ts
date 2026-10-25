import { queryOptions } from "@tanstack/react-query";

import { apiRequest } from "@/shared/api";

import { tenantListSchema } from "../model/schema";

export const tenantKeys = {
  all: ["operator", "tenants"] as const,
  list: () => [...tenantKeys.all, "list"] as const,
};

export const tenantQueries = {
  list: () =>
    queryOptions({
      queryKey: tenantKeys.list(),
      queryFn: ({ signal }) => apiRequest("/tenants", { schema: tenantListSchema, signal }).then((r) => r.items),
    }),
};
