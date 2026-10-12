import { ApiError } from "./http";

export function describeError(error: unknown): string {
  if (error instanceof ApiError && error.status === 501) {
    return error.detail ? `Ещё не реализовано на бэкенде: ${error.detail}` : "Ещё не реализовано на бэкенде";
  }
  if (error instanceof ApiError && error.detail) return `${error.message}: ${error.detail}`;
  return error instanceof Error ? error.message : "Неизвестная ошибка";
}
