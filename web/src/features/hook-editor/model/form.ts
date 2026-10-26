import { z } from "zod";

import {
  HOOK_NAME_MAX,
  HOOK_NAME_PATTERN,
  MEMORY_PAGES,
  TIMEOUT_MS,
  type HookSpec,
  type OperatorHook,
} from "@/entities/hook";

const DRAFT_2020_12 = "https://json-schema.org/draft/2020-12/schema";

function parseObject(text: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(text);
    return value !== null && typeof value === "object" && !Array.isArray(value)
      ? (value as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

const jsonObjectText = z.string().superRefine((text, ctx) => {
  try {
    JSON.parse(text);
  } catch {
    ctx.addIssue({ code: "custom", message: "Некорректный JSON" });
    return;
  }
  if (!parseObject(text)) ctx.addIssue({ code: "custom", message: "Нужен JSON-объект: { … }" });
});

const integerIn = (range: { min: number; max: number }) =>
  z
    .number({ error: "Укажите целое число" })
    .int("Укажите целое число")
    .min(range.min, `Не меньше ${range.min}`)
    .max(range.max, `Не больше ${range.max}`);

export const hookFormSchema = z.object({
  name: z
    .string()
    .min(1, "Укажите имя хука")
    .max(HOOK_NAME_MAX, `Не длиннее ${HOOK_NAME_MAX} символов`)
    .regex(
      HOOK_NAME_PATTERN,
      "Строчные латинские буквы, цифры и _, части через точку, например checkout.discount",
    ),
  timeout_ms: integerIn(TIMEOUT_MS),
  memory_max_pages: integerIn(MEMORY_PAGES),
  input_schema: jsonObjectText,
  output_schema: jsonObjectText,
  sample_input: jsonObjectText,
  allowed_host_functions: z.array(z.string()),
  allowed_effect_types: z.array(z.string()),
});

export type HookFormValues = z.infer<typeof hookFormSchema>;

const pretty = (value: unknown) => JSON.stringify(value, null, 2);

export const emptyHookForm: HookFormValues = {
  name: "",
  timeout_ms: 50,
  memory_max_pages: 64,
  input_schema: pretty({ $schema: DRAFT_2020_12, type: "object", properties: {} }),
  output_schema: pretty({ $schema: DRAFT_2020_12, type: "object", properties: {} }),
  sample_input: pretty({}),
  allowed_host_functions: [],
  allowed_effect_types: [],
};

export function formFromHook(hook: OperatorHook): HookFormValues {
  return {
    name: hook.name,
    timeout_ms: hook.timeout_ms,
    memory_max_pages: hook.memory_max_pages,
    input_schema: pretty(hook.input_schema),
    output_schema: pretty(hook.output_schema),
    sample_input: pretty(hook.sample_input),
    allowed_host_functions: hook.allowed_host_functions,
    allowed_effect_types: hook.allowed_effect_types,
  };
}

export function specFromForm(values: HookFormValues): HookSpec {
  return {
    timeout_ms: values.timeout_ms,
    memory_max_pages: values.memory_max_pages,
    input_schema: parseObject(values.input_schema) ?? {},
    output_schema: parseObject(values.output_schema) ?? {},
    sample_input: parseObject(values.sample_input) ?? {},
    allowed_host_functions: values.allowed_host_functions,
    allowed_effect_types: values.allowed_effect_types,
  };
}

export function formatJson(text: string): string {
  try {
    return pretty(JSON.parse(text));
  } catch {
    return text;
  }
}
