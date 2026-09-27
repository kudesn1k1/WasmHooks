import { z } from "zod";

const jsonSchema = z.record(z.string(), z.unknown());

export const hookSchema = z.object({
  name: z.string(),
  def_version: z.number().int(),
  input_schema: jsonSchema,
  output_schema: jsonSchema,
  timeout_ms: z.number().int().positive(),
  memory_max_pages: z.number().int().positive(),
  allowed_host_functions: z.array(z.string()),
  allowed_effect_types: z.array(z.string()),
  sample_input: z.unknown().optional(),
});
export type Hook = z.infer<typeof hookSchema>;

export const bindingSchema = z.object({
  active_module_id: z.string().nullable(),
  active_module_hash: z.string().nullable(),
  config: z.record(z.string(), z.string()),
  config_version: z.number().int(),
  updated_at: z.iso.datetime({ offset: true }),
});
export type Binding = z.infer<typeof bindingSchema>;

export const tenantHookSchema = z.object({
  hook: hookSchema,
  binding: bindingSchema.nullable(),
});
export type TenantHook = z.infer<typeof tenantHookSchema>;

export const tenantHookListSchema = z.object({ items: z.array(tenantHookSchema) });
