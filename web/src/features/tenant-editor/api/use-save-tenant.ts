import { useMutation, useQueryClient } from "@tanstack/react-query";

import { tenantKeys, tenantSchema, type TenantCreate, type TenantPatch } from "@/entities/tenant";
import { apiRequest } from "@/shared/api";

export function useCreateTenant() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: TenantCreate) => apiRequest("/tenants", { method: "POST", schema: tenantSchema, body }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: tenantKeys.all }),
  });
}

export function usePatchTenant(externalId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: TenantPatch) =>
      apiRequest(`/tenants/${encodeURIComponent(externalId)}`, { method: "PATCH", schema: tenantSchema, body }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: tenantKeys.all }),
  });
}
