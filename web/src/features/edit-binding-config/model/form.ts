import { z } from "zod";

export type BindingConfig = Record<string, string>;

const row = z.object({ key: z.string(), value: z.string() });

const isBlank = (r: z.infer<typeof row>) => r.key.trim() === "" && r.value === "";

export const configFormSchema = z.object({
  rows: z.array(row).superRefine((rows, ctx) => {
    const seen = new Map<string, number>();
    rows.forEach((r, index) => {
      if (isBlank(r)) return;
      const key = r.key.trim();
      if (!key) {
        ctx.addIssue({ code: "custom", path: [index, "key"], message: "Укажите ключ" });
        return;
      }
      if (seen.has(key)) {
        ctx.addIssue({ code: "custom", path: [index, "key"], message: "Такой ключ уже есть" });
        return;
      }
      seen.set(key, index);
    });
  }),
});

export type ConfigFormValues = z.infer<typeof configFormSchema>;

export function formFromConfig(config: BindingConfig | undefined): ConfigFormValues {
  const rows = Object.entries(config ?? {})
    .toSorted(([a], [b]) => a.localeCompare(b))
    .map(([key, value]) => ({ key, value }));
  return { rows: rows.length > 0 ? rows : [{ key: "", value: "" }] };
}

export function configFromForm(values: ConfigFormValues): BindingConfig {
  return Object.fromEntries(values.rows.filter((r) => !isBlank(r)).map((r) => [r.key.trim(), r.value]));
}

export function sameConfig(a: BindingConfig, b: BindingConfig): boolean {
  const keys = Object.keys(a);
  return keys.length === Object.keys(b).length && keys.every((key) => b[key] === a[key]);
}
