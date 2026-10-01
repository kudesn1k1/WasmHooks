import { z } from "zod";

export const moduleStatuses = ["uploaded", "validating", "validated", "rejected", "archived"] as const;
export const moduleStatusSchema = z.enum(moduleStatuses);
export type ModuleStatus = z.infer<typeof moduleStatusSchema>;

export const validationCheckSchema = z.object({
  name: z.enum(["fetch", "compile", "exports", "imports", "sample_call"]),
  ok: z.boolean(),
  detail: z.string().optional(),
});
export type ValidationCheck = z.infer<typeof validationCheckSchema>;

export const validationReportSchema = z.object({
  ok: z.boolean(),
  checks: z.array(validationCheckSchema),
});

export const moduleSchema = z.object({
  id: z.string(),
  hook: z.string(),
  content_hash: z.string(),
  size_bytes: z.number().int().nonnegative(),
  status: moduleStatusSchema,
  validation_report: validationReportSchema.nullable(),
  uploaded_at: z.iso.datetime({ offset: true }),
});
export type Module = z.infer<typeof moduleSchema>;

export const moduleListSchema = z.object({ items: z.array(moduleSchema) });

export function isPending(status: ModuleStatus): boolean {
  return status === "uploaded" || status === "validating";
}

export function firstFailedCheck(module: Module): ValidationCheck | undefined {
  return module.validation_report?.checks.find((c) => !c.ok);
}
