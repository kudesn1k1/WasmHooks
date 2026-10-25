import { useMutation, useQueryClient } from "@tanstack/react-query";
import { z } from "zod";

import { apiKeyKeys } from "@/entities/api-key";
import { apiRequest } from "@/shared/api";

export function useRevokeApiKey(onRevoked?: () => void) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (keyId: string) =>
      apiRequest(`/api-keys/${encodeURIComponent(keyId)}`, { method: "DELETE", schema: z.undefined() }),
    onSuccess: () => {
      onRevoked?.();
      return queryClient.invalidateQueries({ queryKey: apiKeyKeys.all });
    },
  });
}
