import { HttpResponse, type HttpResponseResolver } from "msw";

import { API_BASE_URL } from "@/shared/config/env";

import { db } from "./db";

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
  const match = /^Bearer\s+(\S+)/.exec(request.headers.get("Authorization") ?? "");
  return match !== null && !db.apiKeys.some((key) => key.revoked_at && key.token === match[1]);
}

export function authed(resolver: HttpResponseResolver): HttpResponseResolver {
  return (info) =>
    hasBearer(info.request)
      ? resolver(info)
      : problem(401, "Не авторизован", undefined, { "WWW-Authenticate": "Bearer" });
}
