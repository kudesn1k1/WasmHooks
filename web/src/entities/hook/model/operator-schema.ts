import { z } from "zod";

export const HOOK_NAME_PATTERN = /^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$/;
export const HOOK_NAME_MAX = 128;
export const TIMEOUT_MS = { min: 1, max: 30_000 } as const;
export const MEMORY_PAGES = { min: 16, max: 16_384 } as const;

const jsonObject = z.record(z.string(), z.unknown());
const stringList = z
  .array(z.string())
  .nullish()
  .transform((list) => list ?? []);

export const operatorHookSchema = z.object({
  name: z.string(),
  def_version: z.number().int(),
  input_schema: jsonObject,
  output_schema: jsonObject,
  timeout_ms: z.number().int(),
  memory_max_pages: z.number().int(),
  allowed_host_functions: stringList,
  allowed_effect_types: stringList,
  sample_input: jsonObject,
  created_at: z.iso.datetime({ offset: true }),
  updated_at: z.iso.datetime({ offset: true }),
});
export type OperatorHook = z.infer<typeof operatorHookSchema>;

export const operatorHookListSchema = z.object({ items: z.array(operatorHookSchema) });

export type HookSpec = Pick<
  OperatorHook,
  | "input_schema"
  | "output_schema"
  | "timeout_ms"
  | "memory_max_pages"
  | "allowed_host_functions"
  | "allowed_effect_types"
  | "sample_input"
>;

export type HookCreate = HookSpec & { name: string };
