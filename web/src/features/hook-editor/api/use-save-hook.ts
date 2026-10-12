import { useMutation, useQueryClient } from "@tanstack/react-query";

import {
  operatorHookKeys,
  operatorHookSchema,
  type HookCreate,
  type HookSpec,
  type OperatorHook,
} from "@/entities/hook";
import { apiRequest } from "@/shared/api";

export function useCreateHook() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: HookCreate) =>
      apiRequest("/hooks", { method: "POST", schema: operatorHookSchema, body }),
    onSuccess: (hook) => {
      queryClient.setQueryData(operatorHookKeys.detail(hook.name), hook);
      return queryClient.invalidateQueries({ queryKey: operatorHookKeys.list() });
    },
  });
}

export function useReplaceHook(name: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: HookSpec) =>
      apiRequest(`/hooks/${encodeURIComponent(name)}`, {
        method: "PUT",
        schema: operatorHookSchema,
        body,
      }),
    onSuccess: (hook: OperatorHook) => {
      queryClient.setQueryData(operatorHookKeys.detail(hook.name), hook);
      return queryClient.invalidateQueries({ queryKey: operatorHookKeys.list() });
    },
  });
}
