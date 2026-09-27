import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterAll, afterEach, beforeAll } from "vitest";

import { resetDb } from "@/mocks/db";
import { server } from "@/mocks/node";

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));

afterEach(() => {
  cleanup();
  server.resetHandlers();
  resetDb();
});

afterAll(() => server.close());
