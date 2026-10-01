import type { Binding, Hook } from "@/entities/hook";
import type { Invocation, Outcome } from "@/entities/invocation";
import type { Module, ValidationCheck } from "@/entities/module";

type PendingModule = Module & { validatesAt: number; rejectReason?: string };

type Db = {
  hooks: Hook[];
  modules: PendingModule[];
  bindings: Map<string, Binding>;
  invocations: Invocation[];
};

const HOOKS: Hook[] = [
  {
    name: "checkout.discount",
    def_version: 3,
    timeout_ms: 50,
    memory_max_pages: 64,
    allowed_host_functions: [],
    allowed_effect_types: [],
    input_schema: {
      type: "object",
      required: ["cart_total", "customer"],
      properties: {
        cart_total: { type: "number", minimum: 0 },
        customer: {
          type: "object",
          required: ["id", "lifetime_spend"],
          properties: { id: { type: "string" }, lifetime_spend: { type: "number" } },
        },
      },
    },
    output_schema: {
      type: "object",
      required: ["discount_percent"],
      properties: {
        discount_percent: { type: "integer", minimum: 0, maximum: 100 },
        reason: { type: "string" },
      },
    },
    sample_input: { cart_total: 1500, customer: { id: "c-42", lifetime_spend: 2400 } },
  },
  {
    name: "order.validate",
    def_version: 1,
    timeout_ms: 100,
    memory_max_pages: 64,
    allowed_host_functions: [],
    allowed_effect_types: ["order.flag"],
    input_schema: { type: "object", required: ["order"], properties: { order: { type: "object" } } },
    output_schema: {
      type: "object",
      required: ["valid"],
      properties: { valid: { type: "boolean" }, message: { type: "string" } },
    },
    sample_input: { order: { id: "o-1", items: [] } },
  },
];

const hash = (seed: string) => `sha256:${seed.repeat(8).slice(0, 64)}`;

function initialModules(): PendingModule[] {
  const ok = { ok: true, checks: checksOk() };
  return [
    {
      id: "mod_01",
      hook: "checkout.discount",
      content_hash: hash("3f1a9c2e"),
      size_bytes: 131_204,
      status: "validated",
      validation_report: ok,
      uploaded_at: "2026-09-10T09:12:00Z",
      validatesAt: 0,
    },
    {
      id: "mod_02",
      hook: "checkout.discount",
      content_hash: hash("9b7d04f1"),
      size_bytes: 139_560,
      status: "validated",
      validation_report: ok,
      uploaded_at: "2026-09-18T14:40:00Z",
      validatesAt: 0,
    },
    {
      id: "mod_03",
      hook: "checkout.discount",
      content_hash: hash("c2e88a10"),
      size_bytes: 152_977,
      status: "rejected",
      validation_report: { ok: false, checks: checksRejected(HTTP_IMPORT_DETAIL) },
      uploaded_at: "2026-09-22T11:05:00Z",
      validatesAt: 0,
    },
  ];
}

const HTTP_IMPORT_DETAIL = "import extism:host/env.http_request is not allowed for this hook";

function checksOk(): ValidationCheck[] {
  return [
    { name: "fetch", ok: true },
    { name: "compile", ok: true },
    { name: "exports", ok: true },
    { name: "imports", ok: true },
    { name: "sample_call", ok: true },
  ];
}

function checksRejected(detail: string): ValidationCheck[] {
  return [
    { name: "fetch", ok: true },
    { name: "compile", ok: true },
    { name: "exports", ok: true },
    { name: "imports", ok: false, detail },
    { name: "sample_call", ok: false, detail: "skipped" },
  ];
}

function mulberry32(seed: number) {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const random = mulberry32(42);

const OUTCOME_WEIGHTS: [Outcome, number, string | null, string | null][] = [
  ["ok", 0.86, null, null],
  ["timeout", 0.05, null, null],
  ["handler_error", 0.04, "trap", "wasm error: unreachable"],
  ["handler_error", 0.02, "invalid_output", "result does not match output schema"],
  ["unavailable", 0.02, "pool_saturated", "no free instance for this tenant"],
  ["no_handler", 0.01, null, null],
];

function makeInvocation(seq: number, ts: Date, moduleHash: string | null): Invocation {
  let roll = random();
  let [outcome, , reason, error] = OUTCOME_WEIGHTS[0];
  for (const entry of OUTCOME_WEIGHTS) {
    if (roll < entry[1]) {
      [outcome, , reason, error] = entry;
      break;
    }
    roll -= entry[1];
  }
  const ran = outcome !== "no_handler" && outcome !== "unavailable";
  const duration = outcome === "timeout" ? 50 + random() * 2 : 0.3 + random() * 2.5;
  return {
    seq,
    ts: ts.toISOString(),
    hook: "checkout.discount",
    module_hash: ran ? moduleHash : null,
    outcome,
    reason,
    error,
    duration_ms: Math.round(duration * 100) / 100,
    cold_start: ran && random() < 0.05,
    idempotency_key: `order-${1000 + seq}:checkout.discount`,
    logs: outcome === "ok" ? [`info: discount computed: ${random() < 0.5 ? 0 : 10}`] : [],
  };
}

function initialInvocations(moduleHash: string): Invocation[] {
  const now = Date.now();
  return Array.from({ length: 60 }, (_, i) =>
    makeInvocation(i + 1, new Date(now - (60 - i) * 30_000), moduleHash),
  );
}

function initialDb(): Db {
  const modules = initialModules();
  const active = modules[1];
  return {
    hooks: HOOKS,
    modules,
    bindings: new Map([
      [
        "checkout.discount",
        {
          active_module_id: active.id,
          active_module_hash: active.content_hash,
          config: { threshold: "1000", percent: "10" },
          config_version: 7,
          updated_at: "2026-09-18T14:45:00Z",
        },
      ],
    ]),
    invocations: initialInvocations(active.content_hash),
  };
}

export let db = initialDb();

export function resetDb() {
  db = initialDb();
}

export const VALIDATION_MS = 3_000;

export function settleModules() {
  const now = Date.now();
  for (const m of db.modules) {
    if (m.status !== "validating" || now < m.validatesAt) continue;
    if (m.rejectReason) {
      m.status = "rejected";
      m.validation_report = { ok: false, checks: checksRejected(m.rejectReason) };
    } else {
      m.status = "validated";
      m.validation_report = { ok: true, checks: checksOk() };
    }
  }
}

export function addUploadedModule(hook: string, contentHash: string, size: number, fileName: string) {
  const mod: PendingModule = {
    id: `mod_${String(db.modules.length + 1).padStart(2, "0")}`,
    hook,
    content_hash: contentHash,
    size_bytes: size,
    status: "validating",
    validation_report: null,
    uploaded_at: new Date().toISOString(),
    validatesAt: Date.now() + VALIDATION_MS,
    rejectReason: fileName.includes("http") ? HTTP_IMPORT_DETAIL : undefined,
  };
  db.modules.push(mod);
  return mod;
}

export function tickInvocations() {
  const last = db.invocations.at(-1);
  const binding = db.bindings.get("checkout.discount");
  if (!last || !binding || Date.now() - Date.parse(last.ts) < 4_000) return;
  db.invocations.push(makeInvocation(last.seq + 1, new Date(), binding.active_module_hash));
}

export function publicModule(mod: PendingModule): Module {
  const { id, hook, content_hash, size_bytes, status, validation_report, uploaded_at } = mod;
  return { id, hook, content_hash, size_bytes, status, validation_report, uploaded_at };
}
