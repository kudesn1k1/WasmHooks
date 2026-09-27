import { delay, http, HttpResponse } from "msw";

import type { TenantHook } from "@/entities/hook";
import { outcomeSchema } from "@/entities/invocation";
import { API_BASE_URL } from "@/shared/config/env";

import {
  addUploadedModule,
  db,
  publicModule,
  settleModules,
  tickInvocations,
} from "./db";

const base = API_BASE_URL.startsWith("http") ? API_BASE_URL : `*${API_BASE_URL}`;
const url = (path: string) => `${base}${path}`;

function problem(status: number, title: string, detail?: string) {
  return HttpResponse.json(
    { type: "about:blank", title, status, detail },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

function tenantHook(name: string): TenantHook | undefined {
  const hook = db.hooks.find((h) => h.name === name);
  return hook && { hook, binding: db.bindings.get(name) ?? null };
}

async function sha256(bytes: ArrayBuffer): Promise<string> {
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
  return `sha256:${Array.from(digest, (b) => b.toString(16).padStart(2, "0")).join("")}`;
}

export const handlers = [
  http.get(url("/tenant/hooks"), async () => {
    await delay();
    return HttpResponse.json({ items: db.hooks.map((h) => tenantHook(h.name)) });
  }),

  http.get(url("/tenant/hooks/:hook"), async ({ params }) => {
    await delay();
    const item = tenantHook(String(params.hook));
    return item ? HttpResponse.json(item) : problem(404, "Хук не найден");
  }),

  http.get(url("/tenant/hooks/:hook/modules"), async ({ params }) => {
    await delay();
    const name = String(params.hook);
    if (!tenantHook(name)) return problem(404, "Хук не найден");
    settleModules();
    const items = db.modules
      .filter((m) => m.hook === name)
      .sort((a, b) => b.uploaded_at.localeCompare(a.uploaded_at))
      .map(publicModule);
    return HttpResponse.json({ items });
  }),

  http.post(url("/tenant/hooks/:hook/modules"), async ({ params, request }) => {
    await delay();
    const name = String(params.hook);
    if (!tenantHook(name)) return problem(404, "Хук не найден");
    const file = (await request.formData()).get("file");
    if (!(file instanceof File)) return problem(400, "Нет файла", "Ожидается поле file");
    const bytes = await file.arrayBuffer();
    const contentHash = await sha256(bytes);
    if (db.modules.some((m) => m.hook === name && m.content_hash === contentHash)) {
      return problem(409, "Такая версия уже загружена", contentHash);
    }
    const mod = addUploadedModule(name, contentHash, bytes.byteLength, file.name);
    return HttpResponse.json(publicModule(mod), { status: 201 });
  }),

  http.put(url("/tenant/hooks/:hook/binding"), async ({ params, request }) => {
    await delay();
    const name = String(params.hook);
    if (!tenantHook(name)) return problem(404, "Хук не найден");
    const body = (await request.json()) as { active_module_id?: string };
    const mod = db.modules.find((m) => m.id === body.active_module_id && m.hook === name);
    if (!mod) return problem(404, "Версия не найдена");
    if (mod.status !== "validated") {
      return problem(409, "Активировать можно только проверенную версию", `Статус: ${mod.status}`);
    }
    const previous = db.bindings.get(name);
    const binding = {
      active_module_id: mod.id,
      active_module_hash: mod.content_hash,
      config: previous?.config ?? {},
      config_version: previous?.config_version ?? 1,
      updated_at: new Date().toISOString(),
    };
    db.bindings.set(name, binding);
    return HttpResponse.json(binding);
  }),

  http.get(url("/tenant/invocations"), async ({ request }) => {
    await delay();
    tickInvocations();
    const params = new URL(request.url).searchParams;
    const outcome = outcomeSchema.safeParse(params.get("outcome"));
    const hook = params.get("hook");
    const limit = Number(params.get("limit") ?? 100);
    const items = db.invocations
      .filter((i) => !outcome.success || i.outcome === outcome.data)
      .filter((i) => !hook || i.hook === hook)
      .toReversed()
      .slice(0, limit);
    return HttpResponse.json({ items });
  }),
];
