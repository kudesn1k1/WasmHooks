import { z } from "zod";

export const EXTERNAL_ID_MAX = 128;
export const QUOTA_MAX = 2_147_483_647;

const quota = z
  .number()
  .int()
  .nullish()
  .transform((value) => value ?? 0);

export const tenantSchema = z.object({
  external_id: z.string(),
  name: z.string(),
  rate_limit_rps: quota,
  concurrency_limit: quota,
  created_at: z.iso.datetime({ offset: true }),
  updated_at: z.iso.datetime({ offset: true }),
});
export type Tenant = z.infer<typeof tenantSchema>;

export const tenantListSchema = z.object({ items: z.array(tenantSchema) });

export type TenantQuotas = Pick<Tenant, "rate_limit_rps" | "concurrency_limit">;
export type TenantCreate = Pick<Tenant, "external_id" | "name"> & TenantQuotas;
export type TenantPatch = Partial<Pick<Tenant, "name"> & TenantQuotas>;

export const consoleSessionSchema = z.object({
  token: z.string(),
  expires_at: z.iso.datetime({ offset: true }),
});
export type ConsoleSession = z.infer<typeof consoleSessionSchema>;
