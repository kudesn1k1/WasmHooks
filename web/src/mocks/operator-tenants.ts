import { delay, http, HttpResponse } from "msw";

import { KEY_NAME_MAX, type ApiKey } from "@/entities/api-key";
import { EXTERNAL_ID_MAX, type Tenant } from "@/entities/tenant";

import { db, type StoredApiKey } from "./db";
import { authed, problem, url } from "./http";

type Body = Record<string, unknown>;

const TENANT_NOT_FOUND = "Тенант не найден";
const SESSION_TTL_MS = 60 * 60 * 1000;
const BASE62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz";

const invalid = (detail: string) => problem(422, "Некорректный запрос", detail);

function quotaError(body: Body, field: "rate_limit_rps" | "concurrency_limit") {
  const value = body[field];
  if (value === undefined || value === null) return null;
  if (typeof value !== "number" || !Number.isInteger(value)) return invalid(`body.${field}: Input should be a valid integer`);
  if (value < 0) return invalid(`body.${field}: Input should be greater than or equal to 0`);
  return null;
}

function createError(body: Body) {
  const externalId = body.external_id;
  if (externalId === undefined) return invalid("body.external_id: Field required");
  if (typeof externalId !== "string") return invalid("body.external_id: Input should be a valid string");
  if (externalId.length < 1) return invalid("body.external_id: String should have at least 1 character");
  if (externalId.length > EXTERNAL_ID_MAX) {
    return invalid(`body.external_id: String should have at most ${EXTERNAL_ID_MAX} characters`);
  }
  if (body.name === undefined) return invalid("body.name: Field required");
  if (typeof body.name !== "string") return invalid("body.name: Input should be a valid string");
  return quotaError(body, "rate_limit_rps") ?? quotaError(body, "concurrency_limit");
}

function patchError(body: Body) {
  if (body.name !== undefined && body.name !== null && typeof body.name !== "string") {
    return invalid("body.name: Input should be a valid string");
  }
  return quotaError(body, "rate_limit_rps") ?? quotaError(body, "concurrency_limit");
}

function base64Url(value: unknown): string {
  const bytes = new TextEncoder().encode(JSON.stringify(value));
  return btoa(String.fromCharCode(...bytes)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function consoleToken(externalId: string, expiresAt: Date): string {
  const now = Math.floor(Date.now() / 1000);
  const claims = {
    sub: externalId,
    aud: "tenant-console",
    iss: "wasmhooks-control-plane",
    iat: now,
    exp: Math.floor(expiresAt.getTime() / 1000),
  };
  return `${base64Url({ alg: "HS256", typ: "JWT" })}.${base64Url(claims)}.mock-signature`;
}

function randomText(alphabet: string, length: number): string {
  const bytes = crypto.getRandomValues(new Uint8Array(length));
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join("");
}

function publicKey({ id, name, created_at, revoked_at }: StoredApiKey): ApiKey {
  return { id, name, created_at, revoked_at };
}

export const tenants = [
  http.get(
    url("/tenants"),
    authed(async () => {
      await delay();
      return HttpResponse.json({
        items: db.tenants.toSorted((a, b) => a.external_id.localeCompare(b.external_id)),
      });
    }),
  ),

  http.get(
    url("/tenants/:externalId"),
    authed(async ({ params }) => {
      await delay();
      const tenant = db.tenants.find((t) => t.external_id === params.externalId);
      return tenant ? HttpResponse.json(tenant) : problem(404, TENANT_NOT_FOUND, String(params.externalId));
    }),
  ),

  http.post(
    url("/tenants"),
    authed(async ({ request }) => {
      await delay();
      const body = (await request.json()) as Body;
      const malformed = createError(body);
      if (malformed) return malformed;
      const externalId = String(body.external_id);
      if (db.tenants.some((t) => t.external_id === externalId)) {
        return problem(409, "Тенант с таким external_id уже есть", externalId);
      }
      const now = new Date().toISOString();
      const tenant: Tenant = {
        external_id: externalId,
        name: String(body.name),
        rate_limit_rps: (body.rate_limit_rps as number | undefined) ?? 0,
        concurrency_limit: (body.concurrency_limit as number | undefined) ?? 0,
        created_at: now,
        updated_at: now,
      };
      db.tenants.push(tenant);
      return HttpResponse.json(tenant, { status: 201 });
    }),
  ),

  http.patch(
    url("/tenants/:externalId"),
    authed(async ({ params, request }) => {
      await delay();
      const body = (await request.json()) as Body;
      const malformed = patchError(body);
      if (malformed) return malformed;
      const index = db.tenants.findIndex((t) => t.external_id === params.externalId);
      if (index < 0) return problem(404, TENANT_NOT_FOUND, String(params.externalId));
      const current = db.tenants[index];
      const changes = (["name", "rate_limit_rps", "concurrency_limit"] as const).filter(
        (field) => body[field] !== undefined && body[field] !== null && body[field] !== current[field],
      );
      if (changes.length === 0) return HttpResponse.json(current);
      const updated: Tenant = { ...current, updated_at: new Date().toISOString() };
      for (const field of changes) Object.assign(updated, { [field]: body[field] });
      db.tenants[index] = updated;
      return HttpResponse.json(updated);
    }),
  ),
];

export const consoleSessions = [
  http.post(
    url("/tenants/:externalId/console-sessions"),
    authed(async ({ params }) => {
      await delay();
      const externalId = String(params.externalId);
      if (!db.tenants.some((t) => t.external_id === externalId)) return problem(404, TENANT_NOT_FOUND, externalId);
      const expiresAt = new Date(Date.now() + SESSION_TTL_MS);
      return HttpResponse.json(
        { token: consoleToken(externalId, expiresAt), expires_at: expiresAt.toISOString() },
        { status: 201 },
      );
    }),
  ),
];

export const apiKeys = [
  http.get(
    url("/api-keys"),
    authed(async () => {
      await delay();
      return HttpResponse.json({
        items: db.apiKeys.toSorted((a, b) => a.created_at.localeCompare(b.created_at)).map(publicKey),
      });
    }),
  ),

  http.post(
    url("/api-keys"),
    authed(async ({ request }) => {
      await delay();
      const body = (await request.json()) as Body;
      if (body.name === undefined) return invalid("body.name: Field required");
      if (typeof body.name !== "string") return invalid("body.name: Input should be a valid string");
      if (body.name.length < 1) return invalid("body.name: String should have at least 1 character");
      if (body.name.length > KEY_NAME_MAX) {
        return invalid(`body.name: String should have at most ${KEY_NAME_MAX} characters`);
      }
      const name = body.name;
      if (db.apiKeys.some((k) => k.name === name)) return problem(409, "Ключ с таким именем уже есть", name);
      const key: StoredApiKey = {
        id: `key_${randomText("0123456789abcdef", 16)}`,
        name,
        created_at: new Date().toISOString(),
        revoked_at: null,
        token: `whk_${randomText(BASE62, 32)}`,
      };
      db.apiKeys.push(key);
      return HttpResponse.json({ ...publicKey(key), token: key.token }, { status: 201 });
    }),
  ),

  http.delete(
    url("/api-keys/:keyId"),
    authed(async ({ params }) => {
      await delay();
      const key = db.apiKeys.find((k) => k.id === params.keyId);
      if (!key) return problem(404, "Ключ не найден", String(params.keyId));
      key.revoked_at ??= new Date().toISOString();
      return new HttpResponse(null, { status: 204 });
    }),
  ),
];
