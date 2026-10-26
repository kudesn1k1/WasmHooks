import type { AcceptsContract, Check, ContractSchema } from "./contract";
import type { problemSchema } from "./http";

export type ContractChecks = [Check<AcceptsContract<typeof problemSchema, ContractSchema<"Problem">>>];
