import type { AcceptsContract, Check, ContractSchema, FitsContract } from "@/shared/api/contract";

import type { HookCreate, HookSpec, operatorHookListSchema, operatorHookSchema } from "./operator-schema";
import type { bindingSchema, hookSchema, tenantHookListSchema, tenantHookSchema } from "./schema";

export type ContractChecks = [
  Check<AcceptsContract<typeof hookSchema, ContractSchema<"HookView">>>,
  Check<AcceptsContract<typeof bindingSchema, ContractSchema<"TenantBinding">>>,
  Check<AcceptsContract<typeof tenantHookSchema, ContractSchema<"TenantHook">>>,
  Check<AcceptsContract<typeof tenantHookListSchema, ContractSchema<"TenantHookList">>>,
  Check<AcceptsContract<typeof operatorHookSchema, ContractSchema<"Hook">>>,
  Check<AcceptsContract<typeof operatorHookListSchema, ContractSchema<"HookList">>>,
  Check<FitsContract<HookCreate, ContractSchema<"HookCreate">>>,
  Check<FitsContract<HookSpec, ContractSchema<"HookSpec">>>,
  Check<FitsContract<{ active_module_id: string }, ContractSchema<"BindingUpdate">>>,
  Check<FitsContract<{ config: Record<string, string> }, ContractSchema<"BindingUpdate">>>,
];
