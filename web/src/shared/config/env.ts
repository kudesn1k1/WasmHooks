export const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "/api/v1";

export const API_MOCKING =
  process.env.NODE_ENV === "development" && process.env.NEXT_PUBLIC_API_MOCKING !== "disabled";
