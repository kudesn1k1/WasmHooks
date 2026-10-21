import { delay, http, HttpResponse } from "msw";

import {
  HOOK_NAME_MAX,
  HOOK_NAME_PATTERN,
  MEMORY_PAGES,
  TIMEOUT_MS,
  type HookSpec,
  type OperatorHook,
} from "@/entities/hook";

import { db } from "./db";
import { authed, problem, url } from "./http";

const SPEC_FIELDS = [
  "input_schema",
  "output_schema",
  "timeout_ms",
  "memory_max_pages",
  "allowed_host_functions",
  "allowed_effect_types",
  "sample_input",
] as const;

const REQUIRED = ["input_schema", "output_schema", "timeout_ms", "memory_max_pages", "sample_input"];
const DRAFT_2020_12 = "https://json-schema.org/draft/2020-12/schema";

type Body = Record<string, unknown>;

const isObject = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object" && !Array.isArray(value);

function bodyError(body: Body, allowed: readonly string[], required: readonly string[]) {
  const invalid = (detail: string) => problem(422, "Некорректный запрос", detail);
  for (const key of Object.keys(body)) {
    if (!allowed.includes(key)) return invalid(`body.${key}: Extra inputs are not permitted`);
  }
  for (const key of required) {
    if (!(key in body)) return invalid(`body.${key}: Field required`);
  }
  if ("name" in body) {
    const name = String(body.name);
    if (name.length > HOOK_NAME_MAX) return invalid(`body.name: String should have at most ${HOOK_NAME_MAX} characters`);
    if (!HOOK_NAME_PATTERN.test(name)) {
      return invalid(`body.name: String should match pattern '${HOOK_NAME_PATTERN.source}'`);
    }
  }
  for (const [field, range] of [
    ["timeout_ms", TIMEOUT_MS],
    ["memory_max_pages", MEMORY_PAGES],
  ] as const) {
    const value = body[field];
    if (typeof value !== "number" || !Number.isInteger(value)) return invalid(`body.${field}: Input should be a valid integer`);
    if (value < range.min) return invalid(`body.${field}: Input should be greater than or equal to ${range.min}`);
    if (value > range.max) return invalid(`body.${field}: Input should be less than or equal to ${range.max}`);
  }
  for (const field of ["input_schema", "output_schema", "sample_input"]) {
    if (!isObject(body[field])) return invalid(`body.${field}: Input should be a valid dictionary`);
  }
  return null;
}

function hookError(spec: HookSpec) {
  const invalid = (detail: string) => problem(422, "Невалидное определение хука", detail);
  for (const field of ["input_schema", "output_schema"] as const) {
    const dialect = spec[field].$schema;
    if (dialect !== undefined && dialect !== DRAFT_2020_12) {
      return invalid(`${field}: only draft 2020-12 is supported, $schema must be '${DRAFT_2020_12}'`);
    }
  }
  const required = spec.input_schema.required;
  if (Array.isArray(required)) {
    const missing = required.find((key) => typeof key === "string" && !(key in spec.sample_input));
    if (missing) return invalid(`sample_input: /: '${missing}' is a required property`);
  }
  return null;
}

function specOf(body: Body): HookSpec {
  const sorted = (value: unknown) => [...new Set(Array.isArray(value) ? value.map(String) : [])].sort();
  return {
    input_schema: body.input_schema as Record<string, unknown>,
    output_schema: body.output_schema as Record<string, unknown>,
    timeout_ms: body.timeout_ms as number,
    memory_max_pages: body.memory_max_pages as number,
    sample_input: body.sample_input as Record<string, unknown>,
    allowed_host_functions: sorted(body.allowed_host_functions),
    allowed_effect_types: sorted(body.allowed_effect_types),
  };
}

const sameSpec = (hook: OperatorHook, spec: HookSpec) =>
  SPEC_FIELDS.every((field) => JSON.stringify(hook[field]) === JSON.stringify(spec[field]));

export const operatorHooks = [
  http.get(
    url("/hooks"),
    authed(async () => {
      await delay();
      return HttpResponse.json({ items: [...db.hooks].sort((a, b) => a.name.localeCompare(b.name)) });
    }),
  ),

  http.get(
    url("/hooks/:name"),
    authed(async ({ params }) => {
      await delay();
      const name = String(params.name);
      const hook = db.hooks.find((h) => h.name === name);
      return hook ? HttpResponse.json(hook) : problem(404, "Хук не найден", name);
    }),
  ),

  http.post(
    url("/hooks"),
    authed(async ({ request }) => {
      await delay();
      const body = (await request.json()) as Body;
      const malformed = bodyError(body, ["name", ...SPEC_FIELDS], ["name", ...REQUIRED]);
      if (malformed) return malformed;
      const spec = specOf(body);
      const invalid = hookError(spec);
      if (invalid) return invalid;
      const name = String(body.name);
      if (db.hooks.some((h) => h.name === name)) return problem(409, "Хук с таким именем уже есть", name);
      const now = new Date().toISOString();
      const hook: OperatorHook = { name, def_version: 1, created_at: now, updated_at: now, ...spec };
      db.hooks.push(hook);
      return HttpResponse.json(hook, { status: 201 });
    }),
  ),

  http.put(
    url("/hooks/:name"),
    authed(async ({ params, request }) => {
      await delay();
      const name = String(params.name);
      const body = (await request.json()) as Body;
      const malformed = bodyError(body, SPEC_FIELDS, REQUIRED);
      if (malformed) return malformed;
      const spec = specOf(body);
      const invalid = hookError(spec);
      if (invalid) return invalid;
      const index = db.hooks.findIndex((h) => h.name === name);
      if (index < 0) return problem(404, "Хук не найден", name);
      const current = db.hooks[index];
      const narrowed = [
        ...current.allowed_host_functions.filter((f) => !spec.allowed_host_functions.includes(f)),
        ...current.allowed_effect_types.filter((t) => !spec.allowed_effect_types.includes(t)),
      ].sort();
      if (narrowed.length > 0) {
        return problem(409, "Нельзя сузить список разрешённых возможностей", narrowed.join(", "));
      }
      if (sameSpec(current, spec)) return HttpResponse.json(current);
      const updated: OperatorHook = {
        ...current,
        ...spec,
        def_version: current.def_version + 1,
        updated_at: new Date().toISOString(),
      };
      db.hooks[index] = updated;
      return HttpResponse.json(updated);
    }),
  ),
];
