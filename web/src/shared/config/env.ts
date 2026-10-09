export const API_BASE_URL = "/api/v1";

export type MockingMode = "all" | "partial" | "disabled";

function mockingMode(): MockingMode {
  if (process.env.NODE_ENV !== "development") return "disabled";
  const mode = process.env.NEXT_PUBLIC_API_MOCKING;
  return mode === "partial" || mode === "disabled" ? mode : "all";
}

export const API_MOCKING: MockingMode = mockingMode();
