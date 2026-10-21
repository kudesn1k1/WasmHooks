import { useQueryClient } from "@tanstack/react-query";
import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { clearToken, getToken } from "@/shared/api";
import { renderWithQuery } from "@/shared/lib/test-utils";

import { SignOutButton } from "./sign-out-button";

let client: ReturnType<typeof useQueryClient> | undefined;

function CaptureClient() {
  client = useQueryClient();
  return null;
}

describe("SignOutButton", () => {
  it("clears the console token and its cached data, not the other console's", async () => {
    const user = userEvent.setup();
    renderWithQuery(
      <>
        <CaptureClient />
        <SignOutButton kind="operator" />
      </>,
    );
    act(() => {
      client!.setQueryData(["operator", "hooks", "list"], []);
      client!.setQueryData(["tenant", "hooks", "list"], []);
    });

    await user.click(screen.getByRole("button", { name: "Выйти" }));

    expect(getToken("operator")).toBeNull();
    expect(getToken("tenant")).toBe("tenant-test-token");
    expect(client!.getQueryData(["operator", "hooks", "list"])).toBeUndefined();
    expect(client!.getQueryData(["tenant", "hooks", "list"])).toEqual([]);
    expect(screen.queryByRole("button", { name: "Выйти" })).not.toBeInTheDocument();
  });

  it("is hidden without a session", () => {
    clearToken("operator");
    renderWithQuery(<SignOutButton kind="operator" />);

    expect(screen.queryByRole("button", { name: "Выйти" })).not.toBeInTheDocument();
  });
});
