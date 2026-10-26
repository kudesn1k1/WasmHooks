import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { db } from "@/mocks/db";
import { getToken, setToken } from "@/shared/api";
import { renderWithQuery } from "@/shared/lib/test-utils";

import { OperatorApiKeysPage } from "./operator-api-keys-page";

const row = (name: string) => screen.getByText(name).closest("tr")!;

async function issueKey(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(await screen.findByRole("button", { name: "Выпустить ключ" }));
  const dialog = screen.getByRole("dialog", { name: "Новый ключ установки" });
  await user.type(within(dialog).getByLabelText("Имя ключа"), name);
  await user.click(within(dialog).getByRole("button", { name: "Выпустить" }));
}

describe("OperatorApiKeysPage", () => {
  it("lists active and revoked keys", async () => {
    renderWithQuery(<OperatorApiKeysPage />);

    await screen.findByText("bootstrap");
    expect(within(row("bootstrap")).getByText("Активен")).toBeInTheDocument();
    expect(within(row("bootstrap")).getByRole("button", { name: "Отозвать bootstrap" })).toBeInTheDocument();
    expect(within(row("old-shop-backend")).getByText(/^Отозван /)).toBeInTheDocument();
    expect(within(row("old-shop-backend")).queryByRole("button")).not.toBeInTheDocument();
  });

  it("shows the new token once and copies it", async () => {
    const user = userEvent.setup();
    renderWithQuery(<OperatorApiKeysPage />);

    await issueKey(user, "shop-backend");

    const dialog = await screen.findByRole("dialog", { name: "Ключ shop-backend выпущен" });
    expect(dialog).toHaveTextContent("больше его не покажут");
    const token = within(dialog).getByLabelText("Токен ключа").textContent!;
    expect(token).toMatch(/^whk_[0-9A-Za-z]{32}$/);

    await user.click(within(dialog).getByRole("button", { name: "Скопировать" }));
    expect(await navigator.clipboard.readText()).toBe(token);
    expect(within(dialog).getByRole("button", { name: "Скопировано" })).toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Готово" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(await screen.findByText("shop-backend")).toBeInTheDocument();
    expect(screen.queryByText(token)).not.toBeInTheDocument();
  });

  it("shows a duplicate name next to the field", async () => {
    const user = userEvent.setup();
    renderWithQuery(<OperatorApiKeysPage />);

    await issueKey(user, "old-shop-backend");

    const dialog = screen.getByRole("dialog", { name: "Новый ключ установки" });
    expect(await within(dialog).findByText("Ключ с таким именем уже есть")).toBeInTheDocument();
  });

  it("revokes a key after confirmation", async () => {
    const user = userEvent.setup();
    renderWithQuery(<OperatorApiKeysPage />);

    await issueKey(user, "ci");
    await user.click(await screen.findByRole("button", { name: "Готово" }));
    await user.click(await screen.findByRole("button", { name: "Отозвать ci" }));
    const dialog = screen.getByRole("dialog", { name: "Отозвать ключ ci?" });
    await user.click(within(dialog).getByRole("button", { name: "Отозвать" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(within(row("ci")).getByText(/^Отозван /)).toBeInTheDocument());
  });

  it("signs out when the key in use is revoked", async () => {
    setToken("operator", db.apiKeys[0].token);
    const user = userEvent.setup();
    renderWithQuery(<OperatorApiKeysPage />);

    await user.click(await screen.findByRole("button", { name: "Отозвать bootstrap" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Отозвать" }));

    await waitFor(() => expect(getToken("operator")).toBeNull());
  });
});
