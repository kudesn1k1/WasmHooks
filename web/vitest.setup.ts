import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, beforeEach } from "vitest";

import { resetDb } from "@/mocks/db";
import { server } from "@/mocks/node";
import { setToken } from "@/shared/api";

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));

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
