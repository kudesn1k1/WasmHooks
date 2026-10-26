import { useMutation, useQueryClient } from "@tanstack/react-query";

import { bindingSchema, hookKeys } from "@/entities/hook";
import { apiRequest } from "@/shared/api";

import type { BindingConfig } from "../model/form";

export function useSaveConfig(hook: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (config: BindingConfig) =>
      apiRequest(`/tenant/hooks/${encodeURIComponent(hook)}/binding`, {
        method: "PUT",
        schema: bindingSchema,
        body: { config },
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: hookKeys.all }),
  });
}
