import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { clearToken, getToken } from "@/shared/api";

import { TenantEntryRedirect } from "./tenant-entry-redirect";

const replace = vi.hoisted(() => vi.fn());

vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace }),
}));

afterEach(() => {
  replace.mockReset();
  window.history.replaceState(null, "", "/");
});

describe("TenantEntryRedirect", () => {
  it("keeps the token from the link before leaving the entry page", () => {
    clearToken("tenant");
    window.history.replaceState(null, "", "/tenant#token=jwt-from-shop");

    render(<TenantEntryRedirect to="/tenant/hooks" />);

    expect(getToken("tenant")).toBe("jwt-from-shop");
    expect(window.location.hash).toBe("");
    expect(replace).toHaveBeenCalledWith("/tenant/hooks");
  });
});
