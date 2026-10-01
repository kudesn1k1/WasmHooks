import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { renderWithQuery } from "@/shared/lib/test-utils";

import { TenantHookPage } from "./tenant-hook-page";

const row = (hash: string) => screen.getByText(hash).closest("tr")!;

describe("TenantHookPage", () => {
  it("shows versions with the active one and the rejection reason", async () => {
    renderWithQuery(<TenantHookPage hook="checkout.discount" />);

    await screen.findByText("9b7d04f1");
    expect(within(row("9b7d04f1")).getByText("активна")).toBeInTheDocument();
    expect(within(row("c2e88a10")).getByText("отклонён")).toBeInTheDocument();
    expect(within(row("c2e88a10")).getByText(/http_request is not allowed/)).toBeInTheDocument();
    expect(within(row("c2e88a10")).queryByRole("button")).not.toBeInTheDocument();
  });

  it("rolls back to an older validated version", async () => {
    const user = userEvent.setup();
    renderWithQuery(<TenantHookPage hook="checkout.discount" />);

    await screen.findByText("3f1a9c2e");
    await user.click(within(row("3f1a9c2e")).getByRole("button", { name: "Откатить" }));

    await waitFor(() => expect(within(row("3f1a9c2e")).getByText("активна")).toBeInTheDocument());
    expect(within(row("9b7d04f1")).getByRole("button", { name: "Активировать" })).toBeInTheDocument();
  });

  it("shows an error for an unknown hook", async () => {
    renderWithQuery(<TenantHookPage hook="nope" />);

    expect(await screen.findByRole("alert")).toHaveTextContent("Хук не найден");
  });
});
