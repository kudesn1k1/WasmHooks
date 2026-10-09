import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { z } from "zod";

import { server } from "@/mocks/node";

import { describeError } from "./describe-error";
import { ApiError, apiRequest } from "./http";
import { clearToken, getToken, setToken } from "./session";

const schema = z.object({ name: z.string() });

describe("apiRequest", () => {
  it("returns data that matches the schema", async () => {
    server.use(http.get("*/api/v1/thing", () => HttpResponse.json({ name: "a" })));

    await expect(apiRequest("/thing", { schema })).resolves.toEqual({ name: "a" });
  });

  it("turns problem details into ApiError", async () => {
    server.use(
      http.get("*/api/v1/thing", () =>
        HttpResponse.json({ title: "Хук не найден", status: 404, detail: "x" }, { status: 404 }),
      ),
    );

    const error = await apiRequest("/thing", { schema }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 404, message: "Хук не найден", detail: "x" });
  });

  it("reports a response that breaks the contract", async () => {
    server.use(http.get("*/api/v1/thing", () => HttpResponse.json({ name: 1 })));

    await expect(apiRequest("/thing", { schema })).rejects.toThrow(/не соответствует контракту/);
  });

  it("sends query parameters and skips empty ones", async () => {
    let seen = "";
    server.use(
      http.get("*/api/v1/thing", ({ request }) => {
        seen = new URL(request.url).search;
        return HttpResponse.json({ name: "a" });
      }),
    );

    await apiRequest("/thing", { schema, query: { outcome: "ok", hook: undefined, limit: 10 } });
    expect(seen).toBe("?outcome=ok&limit=10");
  });

  it("accepts null for nullable contract fields", async () => {
    server.use(http.get("*/api/v1/thing", () => HttpResponse.json({ name: "a", note: null })));

    await expect(
      apiRequest("/thing", { schema: schema.extend({ note: z.string().nullish() }) }),
    ).resolves.toEqual({ name: "a", note: null });
  });
});

describe("authorization", () => {
  const seenAuth = (path: string) => {
    let auth: string | null = null;
    server.use(
      http.get(`*/api/v1${path}`, ({ request }) => {
        auth = request.headers.get("Authorization");
        return HttpResponse.json({ name: "a" });
      }),
    );
    return () => auth;
  };

  it("sends the installation key to the operator API", async () => {
    setToken("operator", "whk_operator");
    const auth = seenAuth("/hooks");

    await apiRequest("/hooks", { schema });
    expect(auth()).toBe("Bearer whk_operator");
  });

  it("sends the tenant token to the tenant console API", async () => {
    setToken("tenant", "jwt-tenant");
    const auth = seenAuth("/tenant/hooks");

    await apiRequest("/tenant/hooks", { schema });
    expect(auth()).toBe("Bearer jwt-tenant");
  });

  it("sends no header without a token", async () => {
    clearToken("operator");
    const auth = seenAuth("/hooks");

    await apiRequest("/hooks", { schema });
    expect(auth()).toBeNull();
  });

  it("clears only the token of the console that got 401", async () => {
    server.use(
      http.get("*/api/v1/tenant/hooks", () =>
        HttpResponse.json(
          { type: "about:blank", title: "Не авторизован", status: 401 },
          { status: 401, headers: { "WWW-Authenticate": "Bearer" } },
        ),
      ),
    );

    await expect(apiRequest("/tenant/hooks", { schema })).rejects.toMatchObject({ status: 401 });
    expect(getToken("tenant")).toBeNull();
    expect(getToken("operator")).toBe("whk_test");
  });

  it("mocks answer 401 without a token, like the real API", async () => {
    clearToken("tenant");

    const error = await apiRequest("/tenant/hooks", { schema }).catch((e: unknown) => e);
    expect(error).toMatchObject({ status: 401, message: "Не авторизован" });
  });
});

describe("not implemented", () => {
  it("keeps the backend task from 501 in a readable message", async () => {
    server.use(
      http.get("*/api/v1/tenants", () =>
        HttpResponse.json(
          { type: "about:blank", title: "Ещё не реализовано", status: 501, detail: "T1" },
          { status: 501 },
        ),
      ),
    );

    const error = await apiRequest("/tenants", { schema }).catch((e: unknown) => e);
    expect(error).toMatchObject({ status: 501, detail: "T1" });
    expect(describeError(error)).toBe("Ещё не реализовано на бэкенде: T1");
    expect(getToken("operator")).toBe("whk_test");
  });
});
