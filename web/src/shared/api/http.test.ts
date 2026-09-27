import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { z } from "zod";

import { server } from "@/mocks/node";

import { ApiError, apiRequest } from "./http";

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
});
