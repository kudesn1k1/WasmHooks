import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";

import { addUploadedModule, publicModule, VALIDATION_MS } from "@/mocks/db";
import { server } from "@/mocks/node";
import { renderWithQuery } from "@/shared/lib/test-utils";

import { TenantHookPage } from "./tenant-hook-page";

const row = (hash: string) => screen.getByText(hash).closest("tr")!;

const wasm = (name = "discount.wasm", extra = 0) =>
  new File([new Uint8Array([0x00, 0x61, 0x73, 0x6d, 0x01, 0, 0, 0, extra])], name);

async function uploadFile(user: ReturnType<typeof userEvent.setup>, file: File) {
  await user.click(await screen.findByRole("button", { name: "Загрузить модуль" }));
  await user.upload(screen.getByLabelText("Файл модуля"), file);
}

function acceptUploads(fileName = "discount.wasm") {
  server.use(
    http.post("*/api/v1/tenant/hooks/:hook/modules", ({ params }) => {
      const mod = addUploadedModule(String(params.hook), `sha256:${"1230c6a675e4".repeat(6).slice(0, 64)}`, 9, fileName);
      return HttpResponse.json(publicModule(mod), { status: 201 });
    }),
  );
}

function skipValidation() {
  const now = Date.now();
  vi.spyOn(Date, "now").mockReturnValue(now + VALIDATION_MS + 1);
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("TenantHookPage", () => {
  it("shows versions, the active one and why the latest was rejected", async () => {
    renderWithQuery(<TenantHookPage hook="checkout.discount" />);

    await screen.findByText("9b7d04f19b7d");
    expect(screen.getByText("Активная версия: 9b7d04f19b7d")).toBeInTheDocument();
    expect(within(row("9b7d04f19b7d")).getByText("Активна")).toBeInTheDocument();
    expect(within(row("c2e88a10c2e8")).getByText("Отклонён")).toBeInTheDocument();
    expect(within(row("c2e88a10c2e8")).getByText("Причина ниже")).toBeInTheDocument();

    const panel = screen.getByRole("status");
    expect(panel).toHaveTextContent("Модуль отклонён: запрещённая возможность");
    expect(panel).toHaveTextContent(/http_request is not allowed/);
    expect(within(panel).getByRole("button", { name: "Загрузить исправленный файл" })).toBeInTheDocument();
  });

  it("rolls back to an older validated version after confirmation", async () => {
    const user = userEvent.setup();
    renderWithQuery(<TenantHookPage hook="checkout.discount" />);

    await user.click(await screen.findByRole("button", { name: "Активировать 3f1a9c2e3f1a" }));
    const dialog = screen.getByRole("dialog", { name: "Активировать 3f1a9c2e3f1a?" });
    expect(dialog).toHaveTextContent("вместо 9b7d04f19b7d");
    await user.click(within(dialog).getByRole("button", { name: "Активировать" }));

    await waitFor(() => expect(within(row("3f1a9c2e3f1a")).getByText("Активна")).toBeInTheDocument());
    expect(screen.getByText("Активная версия: 3f1a9c2e3f1a")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Активная версия сохранена");
    expect(screen.getByRole("button", { name: "Активировать 9b7d04f19b7d" })).toBeInTheDocument();
  });

  it("cancels activation without changing anything", async () => {
    const user = userEvent.setup();
    renderWithQuery(<TenantHookPage hook="checkout.discount" />);

    await user.click(await screen.findByRole("button", { name: "Активировать 3f1a9c2e3f1a" }));
    await user.click(screen.getByRole("button", { name: "Отмена" }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(within(row("9b7d04f19b7d")).getByText("Активна")).toBeInTheDocument();
  });

  it("uploads a module, checks it and offers activation", async () => {
    acceptUploads();
    const user = userEvent.setup();
    renderWithQuery(<TenantHookPage hook="checkout.discount" />);

    await uploadFile(user, wasm());
    const dialog = screen.getByRole("dialog", { name: "Файл выбран" });
    expect(dialog).toHaveTextContent("Файл: discount.wasm");
    expect(dialog).toHaveTextContent("Версия 9b7d04f19b7d продолжит работать");
    await user.click(within(dialog).getByRole("button", { name: "Загрузить и проверить" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(await screen.findByText("Проверяется")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Проверяем модуль");

    skipValidation();
    expect(await screen.findByText("Модуль готов к активации", {}, { timeout: 5000 })).toBeInTheDocument();
  });

  it("explains an older rejection on request", async () => {
    acceptUploads("http-client.wasm");
    const user = userEvent.setup();
    renderWithQuery(<TenantHookPage hook="checkout.discount" />);

    await uploadFile(user, wasm("http-client.wasm"));
    await user.click(screen.getByRole("button", { name: "Загрузить и проверить" }));
    skipValidation();
    await waitFor(() => expect(screen.getAllByText("Отклонён")).toHaveLength(2), { timeout: 5000 });

    await user.click(within(row("c2e88a10c2e8")).getByRole("button", { name: "Показать причину" }));

    expect(within(row("c2e88a10c2e8")).getByText("Причина ниже")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("c2e88a10c2e8");
  });

  it("checks the file before uploading", async () => {
    const user = userEvent.setup();
    renderWithQuery(<TenantHookPage hook="checkout.discount" />);

    await uploadFile(user, new File(["hello"], "notes.wasm"));

    expect(screen.getByRole("dialog", { name: "Загрузить модуль" })).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("Это не WebAssembly-модуль");
  });

  it("shows a server rejection in the dialog and lets you pick another file", async () => {
    server.use(
      http.post("*/api/v1/tenant/hooks/:hook/modules", () =>
        HttpResponse.json(
          { type: "about:blank", title: "Такая версия уже загружена", status: 409, detail: "sha256:abc" },
          { status: 409 },
        ),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(<TenantHookPage hook="checkout.discount" />);

    await uploadFile(user, wasm());
    await user.click(screen.getByRole("button", { name: "Загрузить и проверить" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Такая версия уже загружена: sha256:abc");
    expect(screen.getByRole("button", { name: "Выбрать другой файл" })).toBeInTheDocument();
  });

  it("says the upload is unconfirmed when the response is lost", async () => {
    server.use(http.post("*/api/v1/tenant/hooks/:hook/modules", () => HttpResponse.error()));
    const user = userEvent.setup();
    renderWithQuery(<TenantHookPage hook="checkout.discount" />);

    await uploadFile(user, wasm());
    await user.click(screen.getByRole("button", { name: "Загрузить и проверить" }));

    const dialog = await screen.findByRole("dialog", { name: "Не удалось подтвердить загрузку" });
    expect(dialog).toHaveTextContent("Активная версия не изменилась");
    await user.click(within(dialog).getByRole("button", { name: "К версиям" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows an error for an unknown hook", async () => {
    renderWithQuery(<TenantHookPage hook="nope" />);

    expect(await screen.findByRole("alert")).toHaveTextContent("Хук не найден");
  });
});
