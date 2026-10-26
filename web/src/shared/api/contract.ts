import type { z } from "zod";

import type { components } from "./contract.gen";

export type ContractSchema<Name extends keyof components["schemas"]> = components["schemas"][Name];

export type AcceptsContract<Schema extends z.ZodType, Contract> = [Contract] extends [z.input<Schema>] ? true : false;

export type FitsContract<Value, Contract> = [Value] extends [Contract] ? true : false;

export type Check<T extends true> = T;
