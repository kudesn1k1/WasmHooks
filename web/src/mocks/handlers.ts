import { delay, http, HttpResponse, type RequestHandler } from "msw";

import type { TenantHook } from "@/entities/hook";
import { outcomeSchema } from "@/entities/invocation";
import { MAX_MODULE_BYTES } from "@/entities/module";

import {
  addUploadedModule,
  db,
  publicModule,
  settleModules,
  tickInvocations,
} from "./db";
import { authed, problem, url } from "./http";
import { operatorHooks } from "./operator-hooks";

function tenantHook(name: string): TenantHook | undefined {
  const hook = db.hooks.find((h) => h.name === name);
  return hook && { hook, binding: db.bindings.get(name) ?? null };
}

async function sha256(bytes: ArrayBuffer): Promise<string> {
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
  return `sha256:${Array.from(digest, (b) => b.toString(16).padStart(2, "0")).join("")}`;
}

const tenantHooks = [
  http.get(
    url("/tenant/hooks"),
    authed(async () => {
      await delay();
      return HttpResponse.json({ items: db.hooks.map((h) => tenantHook(h.name)) });
    }),
  ),

  http.get(
    url("/tenant/hooks/:hook"),
    authed(async ({ params }) => {
      await delay();
      const name = String(params.hook);
      const item = tenantHook(name);
      return item ? HttpResponse.json(item) : problem(404, "Хук не найден", name);
    }),
  ),
];

const tenantModules = [
  http.get(
    url("/tenant/hooks/:hook/modules"),
    authed(async ({ params }) => {
      await delay();
      const name = String(params.hook);
      if (!tenantHook(name)) return problem(404, "Хук не найден", name);
      settleModules();
      const items = db.modules
        .filter((m) => m.hook === name)
        .sort((a, b) => b.uploaded_at.localeCompare(a.uploaded_at))
        .map(publicModule);
      return HttpResponse.json({ items });
    }),
  ),

  http.post(
    url("/tenant/hooks/:hook/modules"),
    authed(async ({ params, request }) => {
      await delay();
      const name = String(params.hook);
      if (!tenantHook(name)) return problem(404, "Хук не найден", name);
      const file = (await request.formData()).get("file");
      if (file === null || typeof file === "string") return problem(422, "Некорректный запрос", "body.file: Field required");
      if (file.size > MAX_MODULE_BYTES) return problem(413, "Модуль слишком большой");
      if (file.size === 0) return problem(422, "Пустой модуль");
      const bytes = await file.arrayBuffer();
      const contentHash = await sha256(bytes);
      if (db.modules.some((m) => m.hook === name && m.content_hash === contentHash)) {
        return problem(409, "Такая версия уже загружена", contentHash);
      }
      const mod = addUploadedModule(name, contentHash, bytes.byteLength, file.name);
      return HttpResponse.json(publicModule(mod), { status: 201 });
    }),
  ),
];

const tenantBinding = [
  http.put(
    url("/tenant/hooks/:hook/binding"),
    authed(async ({ params, request }) => {
      await delay();
      const name = String(params.hook);
      if (!tenantHook(name)) return problem(404, "Хук не найден", name);
      const body = (await request.json()) as { active_module_id?: string | null; config?: Record<string, string> | null };
      if (body.active_module_id == null && body.config == null) return problem(400, "Нечего менять");
      const previous = db.bindings.get(name);
      let mod: (typeof db.modules)[number] | undefined;
      if (body.active_module_id != null) {
        mod = db.modules.find((m) => m.id === body.active_module_id && m.hook === name);
        if (!mod) return problem(404, "Версия не найдена", body.active_module_id);
        if (mod.status !== "validated") {
          return problem(409, "Активировать можно только проверенную версию", `Статус: ${mod.status}`);
        }
      }
      const configChanged =
        previous !== undefined && body.config != null && JSON.stringify(body.config) !== JSON.stringify(previous.config);
      const moduleChanged = mod !== undefined && mod.id !== previous?.active_module_id;
      if (previous && !configChanged && !moduleChanged) return HttpResponse.json(previous);
      const binding = {
        active_module_id: mod?.id ?? previous?.active_module_id ?? null,
        active_module_hash: mod?.content_hash ?? previous?.active_module_hash ?? null,
        config: body.config ?? previous?.config ?? {},
        config_version: previous ? previous.config_version + (configChanged ? 1 : 0) : 1,
        updated_at: new Date().toISOString(),
      };
      db.bindings.set(name, binding);
      return HttpResponse.json(binding);
    }),
  ),
];

const invocations = [
  http.get(
    url("/tenant/invocations"),
    authed(async ({ request }) => {
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
  ),
];

export const handlerGroups = {
  operatorHooks,
  tenantHooks,
  tenantModules,
  tenantBinding,
  invocations,
} satisfies Record<string, RequestHandler[]>;

export type HandlerGroup = keyof typeof handlerGroups;

export const handlers: RequestHandler[] = Object.values(handlerGroups).flat();
