import { z } from "zod";

const claimsSchema = z.object({
  sub: z.string().nullish(),
  exp: z.number().nullish(),
});

export type JwtClaims = { sub: string | null; exp: number | null };

function decodeBase64Url(segment: string): string {
  const base64 = segment.replace(/-/g, "+").replace(/_/g, "/");
  const padded = base64.padEnd(Math.ceil(base64.length / 4) * 4, "=");
  const bytes = Uint8Array.from(atob(padded), (char) => char.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

export function readJwtClaims(token: string): JwtClaims | null {
  const payload = token.split(".")[1];
  if (!payload) return null;
  try {
    const parsed = claimsSchema.safeParse(JSON.parse(decodeBase64Url(payload)));
    if (!parsed.success) return null;
    return { sub: parsed.data.sub ?? null, exp: parsed.data.exp ?? null };
  } catch {
    return null;
  }
}

export function isJwtExpired(token: string, now: number): boolean {
  const exp = readJwtClaims(token)?.exp;
  return exp != null && exp * 1000 <= now;
}
