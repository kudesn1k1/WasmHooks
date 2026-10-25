import "@testing-library/jest-dom/vitest";
import { File as NodeFile } from "node:buffer";

import { cleanup } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, beforeEach } from "vitest";

import { resetDb } from "@/mocks/db";
import { server } from "@/mocks/node";
import { setToken } from "@/shared/api";

const NodeFormData = (await new Response(new URLSearchParams("a=1")).formData()).constructor as typeof FormData;

async function toNodeFormData(form: FormData): Promise<FormData> {
  const result = new NodeFormData();
  for (const [key, value] of form.entries()) {
    if (typeof value === "string") result.append(key, value);
    else result.append(key, new NodeFile([new Uint8Array(await value.arrayBuffer())], value.name, { type: value.type }) as unknown as Blob, value.name);
  }
  return result;
}

function withNodeFormData(fetchImpl: typeof fetch): typeof fetch {
  return async (input, init) =>
    init?.body instanceof FormData && !(init.body instanceof NodeFormData)
      ? fetchImpl(input, { ...init, body: await toNodeFormData(init.body) })
      : fetchImpl(input, init);
}

beforeAll(() => {
  server.listen({ onUnhandledRequest: "error" });
  globalThis.fetch = withNodeFormData(globalThis.fetch);
});

beforeEach(() => {
  setToken("operator", "whk_test");
  setToken("tenant", "tenant-test-token");
});

afterEach(() => {
  cleanup();
  server.resetHandlers();
  resetDb();
  sessionStorage.clear();
});

afterAll(() => server.close());
