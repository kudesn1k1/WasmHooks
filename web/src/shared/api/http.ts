import { z } from "zod";

import { API_BASE_URL, SHOP_API_BASE_URL } from "@/shared/config/env";

import { clearToken, consoleForPath, getToken } from "./session";

export const problemSchema = z.object({
  type: z.string().nullish(),
  title: z.string().nullish(),
  status: z.number().nullish(),
  detail: z.string().nullish(),
});

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly detail?: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

type RequestOptions<T> = {
  schema: z.ZodType<T>;
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  query?: Record<string, string | number | undefined>;
  signal?: AbortSignal;
  token?: string;
  service?: "shop";
};

export function apiUrl(
  path: string,
  query?: RequestOptions<unknown>["query"],
  service?: RequestOptions<unknown>["service"],
): URL {
  const origin = typeof window === "undefined" ? "http://localhost" : window.location.origin;
  const base = service === "shop" ? SHOP_API_BASE_URL : API_BASE_URL;
  const url = new URL(base + path, origin);
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value !== undefined && value !== "") url.searchParams.set(key, String(value));
  }
  return url;
}

export async function apiRequest<T>(path: string, options: RequestOptions<T>): Promise<T> {
  const { schema, method = "GET", body, query, signal, service } = options;
  const isForm = body instanceof FormData;
  const kind = consoleForPath(path);
  const token = service === "shop" ? null : options.token ?? getToken(kind);

  const headers = new Headers();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  if (body !== undefined && !isForm) headers.set("Content-Type", "application/json");

  const response = await fetch(apiUrl(path, query, service), {
    method,
    signal,
    headers,
    body: body === undefined ? undefined : isForm ? body : JSON.stringify(body),
  });

  if (!response.ok) {
    if (response.status === 401 && service !== "shop" && options.token === undefined) clearToken(kind);
    const problem = problemSchema.safeParse(await response.json().catch(() => null));
    const title = problem.success ? problem.data.title : undefined;
    const detail = problem.success ? problem.data.detail : undefined;
    throw new ApiError(response.status, title ?? `HTTP ${response.status}`, detail ?? undefined);
  }

  const payload = response.status === 204 ? undefined : await response.json();
  const parsed = schema.safeParse(payload);
  if (!parsed.success) {
    throw new ApiError(
      response.status,
      `Ответ ${method} ${path} не соответствует контракту`,
      z.prettifyError(parsed.error),
    );
  }
  return parsed.data;
}
