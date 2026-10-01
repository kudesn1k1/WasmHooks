import { z } from "zod";

import { API_BASE_URL } from "@/shared/config/env";

const problemSchema = z.object({
  type: z.string().optional(),
  title: z.string().optional(),
  status: z.number().optional(),
  detail: z.string().optional(),
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
  method?: "GET" | "POST" | "PUT" | "DELETE";
  body?: unknown;
  query?: Record<string, string | number | undefined>;
  signal?: AbortSignal;
};

export function apiUrl(path: string, query?: RequestOptions<unknown>["query"]): URL {
  const origin = typeof window === "undefined" ? "http://localhost" : window.location.origin;
  const url = new URL(API_BASE_URL + path, origin);
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value !== undefined && value !== "") url.searchParams.set(key, String(value));
  }
  return url;
}

export async function apiRequest<T>(path: string, options: RequestOptions<T>): Promise<T> {
  const { schema, method = "GET", body, query, signal } = options;
  const isForm = body instanceof FormData;

  const response = await fetch(apiUrl(path, query), {
    method,
    signal,
    credentials: "include",
    headers: body !== undefined && !isForm ? { "Content-Type": "application/json" } : undefined,
    body: body === undefined ? undefined : isForm ? body : JSON.stringify(body),
  });

  if (!response.ok) {
    const problem = problemSchema.safeParse(await response.json().catch(() => null));
    const title = problem.success ? problem.data.title : undefined;
    const detail = problem.success ? problem.data.detail : undefined;
    throw new ApiError(response.status, title ?? `HTTP ${response.status}`, detail);
  }

  const parsed = schema.safeParse(await response.json());
  if (!parsed.success) {
    throw new ApiError(
      response.status,
      `Ответ ${method} ${path} не соответствует контракту`,
      z.prettifyError(parsed.error),
    );
  }
  return parsed.data;
}
