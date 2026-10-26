import { useMutation, useQueryClient } from "@tanstack/react-query";

import { apiKeyCreatedSchema, apiKeyKeys } from "@/entities/api-key";
import { apiRequest } from "@/shared/api";

export function useIssueApiKey() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiRequest("/api-keys", { method: "POST", schema: apiKeyCreatedSchema, body: { name } }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: apiKeyKeys.all }),
  });
}
