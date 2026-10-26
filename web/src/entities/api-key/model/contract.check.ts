import type { AcceptsContract, Check, ContractSchema, FitsContract } from "@/shared/api/contract";

import type { apiKeyCreatedSchema, apiKeyListSchema, apiKeySchema } from "./schema";

export type ContractChecks = [
  Check<AcceptsContract<typeof apiKeySchema, ContractSchema<"ApiKey">>>,
  Check<AcceptsContract<typeof apiKeyListSchema, ContractSchema<"ApiKeyList">>>,
  Check<AcceptsContract<typeof apiKeyCreatedSchema, ContractSchema<"ApiKeyCreated">>>,
  Check<FitsContract<{ name: string }, ContractSchema<"ApiKeyCreate">>>,
];
