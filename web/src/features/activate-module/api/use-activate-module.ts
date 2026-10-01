import { useMutation, useQueryClient } from "@tanstack/react-query";

import { bindingSchema, hookKeys } from "@/entities/hook";
import { moduleKeys } from "@/entities/module";
import { apiRequest } from "@/shared/api";

export function useActivateModule(hook: string) {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (moduleId: string) =>
      apiRequest(`/tenant/hooks/${encodeURIComponent(hook)}/binding`, {
        method: "PUT",
        schema: bindingSchema,
        body: { active_module_id: moduleId },
      }),
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: hookKeys.all }),
        queryClient.invalidateQueries({ queryKey: moduleKeys.byHook(hook) }),
      ]),
  });
}
