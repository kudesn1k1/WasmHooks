import type { AcceptsContract, Check, ContractSchema } from "@/shared/api/contract";

import type { moduleListSchema, moduleSchema, validationCheckSchema, validationReportSchema } from "./schema";

export type ContractChecks = [
  Check<AcceptsContract<typeof validationCheckSchema, ContractSchema<"ValidationCheck">>>,
  Check<AcceptsContract<typeof validationReportSchema, ContractSchema<"ValidationReport">>>,
  Check<AcceptsContract<typeof moduleSchema, ContractSchema<"Module">>>,
  Check<AcceptsContract<typeof moduleListSchema, ContractSchema<"ModuleList">>>,
];
