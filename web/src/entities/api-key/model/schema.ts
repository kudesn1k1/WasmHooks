import { z } from "zod";

export const KEY_NAME_MAX = 64;

export const apiKeySchema = z.object({
  id: z.string(),
  name: z.string(),
  created_at: z.iso.datetime({ offset: true }),
  revoked_at: z.iso.datetime({ offset: true }).nullish(),
});
export type ApiKey = z.infer<typeof apiKeySchema>;

export const apiKeyListSchema = z.object({ items: z.array(apiKeySchema) });

export const apiKeyCreatedSchema = apiKeySchema.extend({ token: z.string() });
export type ApiKeyCreated = z.infer<typeof apiKeyCreatedSchema>;
