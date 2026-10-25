import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";

import { server } from "@/mocks/node";
import { readJwtClaims } from "@/shared/lib/jwt";
import { renderWithQuery } from "@/shared/lib/test-utils";

import { OperatorTenantsPage } from "./operator-tenants-page";

const row = (externalId: string) => screen.getByText(externalId).closest("tr")!;

function recordPatches() {
  const bodies: unknown[] = [];
  server.use(
    http.patch("*/api/v1/tenants/:externalId", async ({ request }) => {
      bodies.push(await request.clone().json());
    }),
  );
  return bodies;
}

function fakeTab() {
  return {
    opener: {},
    location: { href: "" },
    close: vi.fn(),
    sessionStorage: { clear: vi.fn() },
  };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("OperatorTenantsPage", () => {
  it("lists tenants with their quotas", async () => {
    renderWithQuery(<OperatorTenantsPage />);

    await screen.findByText("merchant-a");
    expect(within(row("merchant-a")).getAllByText("по умолчанию")).toHaveLength(2);
    expect(within(row("merchant-b")).getByText("50 rps")).toBeInTheDocument();
    expect(within(row("merchant-b")).getByText("4 шт.")).toBeInTheDocument();
  });

  it("creates a tenant", async () => {
    const user = userEvent.setup();
    renderWithQuery(<OperatorTenantsPage />);

    await user.click(await screen.findByRole("button", { name: "Добавить тенанта" }));
    const dialog = screen.getByRole("dialog", { name: "Новый тенант" });
    await user.type(within(dialog).getByLabelText("external_id"), "merchant-c");
    await user.type(within(dialog).getByLabelText("Название"), "Магазин C");
    await user.clear(within(dialog).getByLabelText("Параллельных вызовов"));
    await user.type(within(dialog).getByLabelText("Параллельных вызовов"), "8");
    await user.click(within(dialog).getByRole("button", { name: "Создать" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(await screen.findByText("merchant-c")).toBeInTheDocument();
    expect(within(row("merchant-c")).getByText("8 шт.")).toBeInTheDocument();
  });

  it("checks the form before sending", async () => {
    const user = userEvent.setup();
    renderWithQuery(<OperatorTenantsPage />);

    await user.click(await screen.findByRole("button", { name: "Добавить тенанта" }));
    const dialog = screen.getByRole("dialog", { name: "Новый тенант" });
    await user.clear(within(dialog).getByLabelText("Лимит запросов в секунду"));
    await user.type(within(dialog).getByLabelText("Лимит запросов в секунду"), "-1");
    await user.click(within(dialog).getByRole("button", { name: "Создать" }));

    expect(await within(dialog).findByText("Укажите external_id")).toBeInTheDocument();
    expect(within(dialog).getByText("Укажите название")).toBeInTheDocument();
    expect(within(dialog).getByText("Не меньше 0")).toBeInTheDocument();
  });

  it("shows a duplicate external_id next to the field", async () => {
    const user = userEvent.setup();
    renderWithQuery(<OperatorTenantsPage />);

    await user.click(await screen.findByRole("button", { name: "Добавить тенанта" }));
    const dialog = screen.getByRole("dialog", { name: "Новый тенант" });
    await user.type(within(dialog).getByLabelText("external_id"), "merchant-a");
    await user.type(within(dialog).getByLabelText("Название"), "Дубль");
    await user.click(within(dialog).getByRole("button", { name: "Создать" }));

    expect(await within(dialog).findByText("Тенант с таким external_id уже есть")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("external_id")).toHaveAttribute("aria-invalid", "true");
  });

  it("sends only the changed fields", async () => {
    const patches = recordPatches();
    const user = userEvent.setup();
    renderWithQuery(<OperatorTenantsPage />);

    await user.click(await screen.findByRole("button", { name: "Изменить merchant-b" }));
    const dialog = screen.getByRole("dialog", { name: "Тенант merchant-b" });
    expect(within(dialog).queryByLabelText("external_id")).not.toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Сохранить" })).toBeDisabled();

    await user.clear(within(dialog).getByLabelText("Лимит запросов в секунду"));
    await user.type(within(dialog).getByLabelText("Лимит запросов в секунду"), "0");
    await user.click(within(dialog).getByRole("button", { name: "Сохранить" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(patches).toEqual([{ rate_limit_rps: 0 }]);
    await waitFor(() => expect(within(row("merchant-b")).getByText("по умолчанию")).toBeInTheDocument());
  });

  it("opens the tenant console in a new tab", async () => {
    const tab = fakeTab();
    vi.spyOn(window, "open").mockReturnValue(tab as unknown as Window);
    const user = userEvent.setup();
    renderWithQuery(<OperatorTenantsPage />);

    await user.click(await screen.findByRole("button", { name: "Открыть консоль merchant-b" }));

    await waitFor(() => expect(tab.location.href).toContain("/tenant#token="));
    const token = decodeURIComponent(tab.location.href.split("#token=")[1]);
    expect(readJwtClaims(token)?.sub).toBe("merchant-b");
    expect(tab.opener).toBeNull();
    expect(tab.sessionStorage.clear).toHaveBeenCalled();
  });

  it("explains a blocked popup", async () => {
    vi.spyOn(window, "open").mockReturnValue(null);
    const user = userEvent.setup();
    renderWithQuery(<OperatorTenantsPage />);

    await user.click(await screen.findByRole("button", { name: "Открыть консоль merchant-a" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Разрешите всплывающие окна");
  });
});
