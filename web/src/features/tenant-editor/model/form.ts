import { z } from "zod";

import { EXTERNAL_ID_MAX, QUOTA_MAX, type Tenant, type TenantPatch } from "@/entities/tenant";

const quota = z
  .number({ error: "Укажите целое число" })
  .int("Укажите целое число")
  .min(0, "Не меньше 0")
  .max(QUOTA_MAX, `Не больше ${QUOTA_MAX}`);

export const tenantFormSchema = z.object({
  external_id: z
    .string()
    .min(1, "Укажите external_id")
    .max(EXTERNAL_ID_MAX, `Не длиннее ${EXTERNAL_ID_MAX} символов`)
    .refine((value) => value.trim() === value, "Без пробелов в начале и в конце"),
  name: z.string().trim().min(1, "Укажите название"),
  rate_limit_rps: quota,
  concurrency_limit: quota,
});

export type TenantFormValues = z.infer<typeof tenantFormSchema>;

export const emptyTenantForm: TenantFormValues = {
  external_id: "",
  name: "",
  rate_limit_rps: 0,
  concurrency_limit: 0,
};

export function formFromTenant(tenant: Tenant): TenantFormValues {
  const { external_id, name, rate_limit_rps, concurrency_limit } = tenant;
  return { external_id, name, rate_limit_rps, concurrency_limit };
}

export function tenantChanges(tenant: Tenant, values: TenantFormValues): TenantPatch {
  const changes: TenantPatch = {};
  if (values.name !== tenant.name) changes.name = values.name;
  if (values.rate_limit_rps !== tenant.rate_limit_rps) changes.rate_limit_rps = values.rate_limit_rps;
  if (values.concurrency_limit !== tenant.concurrency_limit) changes.concurrency_limit = values.concurrency_limit;
  return changes;
}
