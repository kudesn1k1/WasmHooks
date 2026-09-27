import { z } from "zod";

export const outcomes = [
  "ok",
  "no_handler",
  "timeout",
  "handler_error",
  "quota_exceeded",
  "unavailable",
] as const;
export const outcomeSchema = z.enum(outcomes);
export type Outcome = z.infer<typeof outcomeSchema>;

export const invocationSchema = z.object({
  seq: z.number().int(),
  ts: z.iso.datetime({ offset: true }),
  hook: z.string(),
  module_hash: z.string().nullable(),
  outcome: outcomeSchema,
  reason: z.string().nullable(),
  error: z.string().nullable(),
  duration_ms: z.number().nonnegative(),
  cold_start: z.boolean(),
  idempotency_key: z.string().nullable(),
  logs: z.array(z.string()),
});
export type Invocation = z.infer<typeof invocationSchema>;

export const invocationListSchema = z.object({ items: z.array(invocationSchema) });
