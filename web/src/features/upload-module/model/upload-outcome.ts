import { ApiError } from "@/shared/api";

export function isUploadUnconfirmed(error: unknown): boolean {
  if (!(error instanceof ApiError)) return true;
  if (error.status < 400) return true;
  return error.status >= 500 && error.status !== 501;
}
