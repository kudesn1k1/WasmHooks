import { HttpResponse, type HttpResponseResolver } from "msw";

import { API_BASE_URL } from "@/shared/config/env";

export const url = (path: string) => `*${API_BASE_URL}${path}`;

export function problem(
  status: number,
  title: string,
  detail?: string,
  headers?: Record<string, string>,
) {
  return HttpResponse.json(
    { type: "about:blank", title, status, ...(detail === undefined ? {} : { detail }) },
    { status, headers: { "Content-Type": "application/problem+json", ...headers } },
  );
}

function hasBearer(request: Request): boolean {
  const header = request.headers.get("Authorization") ?? "";
  return /^Bearer\s+\S+/.test(header);
}

export function authed(resolver: HttpResponseResolver): HttpResponseResolver {
  return (info) =>
    hasBearer(info.request)
      ? resolver(info)
      : problem(401, "Не авторизован", undefined, { "WWW-Authenticate": "Bearer" });
}
