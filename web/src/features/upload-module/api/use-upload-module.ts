import { useMutation, useQueryClient } from "@tanstack/react-query";

import { moduleKeys, moduleSchema } from "@/entities/module";
import { apiRequest } from "@/shared/api";

export function useUploadModule(hook: string) {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (file: File) => {
      const body = new FormData();
      body.append("file", file);
      return apiRequest(`/tenant/hooks/${encodeURIComponent(hook)}/modules`, {
        method: "POST",
        schema: moduleSchema,
        body,
      });
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: moduleKeys.byHook(hook) }),
  });
}
