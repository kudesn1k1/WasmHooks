import type { AcceptsContract, Check, ContractSchema, FitsContract } from "@/shared/api/contract";

import type { consoleSessionSchema, TenantCreate, tenantListSchema, TenantPatch, tenantSchema } from "./schema";

export type ContractChecks = [
  Check<AcceptsContract<typeof tenantSchema, ContractSchema<"Tenant">>>,
  Check<AcceptsContract<typeof tenantListSchema, ContractSchema<"TenantList">>>,
  Check<AcceptsContract<typeof consoleSessionSchema, ContractSchema<"ConsoleSession">>>,
  Check<FitsContract<TenantCreate, ContractSchema<"TenantCreate">>>,
  Check<FitsContract<TenantPatch, ContractSchema<"TenantPatch">>>,
];
