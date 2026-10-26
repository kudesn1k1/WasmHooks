import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { describe, expect, it, vi } from "vitest";

import { db } from "@/mocks/db";
import { server } from "@/mocks/node";
import { renderWithQuery } from "@/shared/lib/test-utils";

import { HookForm } from "./hook-form";

const hookNamed = (name: string) => structuredClone(db.hooks.find((h) => h.name === name)!);

async function replaceText(user: ReturnType<typeof userEvent.setup>, label: string, text: string) {
  const field = screen.getByLabelText(label);
  await user.clear(field);
  await user.click(field);
  await user.paste(text);
}

describe("HookForm: create", () => {
  it("creates a hook", async () => {
    const user = userEvent.setup();
    const onCreated = vi.fn();
    renderWithQuery(<HookForm onCreated={onCreated} />);

    await user.type(screen.getByLabelText("Имя хука"), "order.refund");
    await user.click(screen.getByRole("button", { name: "Создать хук" }));

    await waitFor(() => expect(onCreated).toHaveBeenCalledOnce());
    expect(onCreated.mock.calls[0][0]).toMatchObject({ name: "order.refund", def_version: 1 });
    expect(db.hooks.map((h) => h.name)).toContain("order.refund");
  });

  it("checks the name on the client before sending", async () => {
    const user = userEvent.setup();
    const onCreated = vi.fn();
    renderWithQuery(<HookForm onCreated={onCreated} />);

    await user.type(screen.getByLabelText("Имя хука"), "Order-Refund");
    await user.click(screen.getByRole("button", { name: "Создать хук" }));

    expect(await screen.findByText(/Строчные латинские буквы/)).toBeInTheDocument();
    expect(onCreated).not.toHaveBeenCalled();
    expect(db.hooks.map((h) => h.name)).not.toContain("Order-Refund");
  });

  it("shows 409 for a taken name next to the name field", async () => {
    const user = userEvent.setup();
    renderWithQuery(<HookForm />);

    await user.type(screen.getByLabelText("Имя хука"), "checkout.discount");
    await user.click(screen.getByRole("button", { name: "Создать хук" }));

    expect(await screen.findByText("Хук с таким именем уже есть")).toBeInTheDocument();
    expect(screen.getByLabelText("Имя хука")).toHaveAttribute("aria-invalid", "true");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows 422 from the server with its detail above the form", async () => {
    const user = userEvent.setup();
    renderWithQuery(<HookForm />);

    await user.type(screen.getByLabelText("Имя хука"), "order.refund");
    await replaceText(user, "Схема входа (JSON Schema 2020-12)", '{"type":"object","required":["amount"]}');
    await user.click(screen.getByRole("button", { name: "Создать хук" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Невалидное определение хука");
    expect(alert).toHaveTextContent("sample_input: /: 'amount' is a required property");
  });

  it("rejects JSON that is not an object", async () => {
    const user = userEvent.setup();
    renderWithQuery(<HookForm />);

    await replaceText(user, "Пример входа", "[1, 2]");
    await user.click(screen.getByRole("button", { name: "Создать хук" }));

    expect(await screen.findByText("Нужен JSON-объект: { … }")).toBeInTheDocument();
  });
});

describe("HookForm: edit", () => {
  it("saves a change and shows the new def_version", async () => {
    const user = userEvent.setup();
    renderWithQuery(<HookForm hook={hookNamed("order.validate")} />);

    const timeout = screen.getByLabelText("Лимит времени, мс");
    await user.clear(timeout);
    await user.type(timeout, "200");
    await user.click(screen.getByRole("button", { name: "Сохранить" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Сохранено. Версия определения: 2");
  });

  it("says there are no changes when nothing changed", async () => {
    const user = userEvent.setup();
    renderWithQuery(<HookForm hook={hookNamed("order.validate")} />);

    await user.click(screen.getByRole("button", { name: "Сохранить" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Изменений нет");
  });

  it("does not let saved allowed values be removed, only new ones", async () => {
    const user = userEvent.setup();
    renderWithQuery(<HookForm hook={hookNamed("order.validate")} />);

    expect(screen.queryByRole("button", { name: "Убрать order.flag" })).not.toBeInTheDocument();

    await user.type(screen.getByLabelText("Разрешённые типы эффектов"), "order.hold{Enter}");
    expect(screen.getByRole("button", { name: "Убрать order.hold" })).toBeInTheDocument();
  });

  it("shows the removed values when the server refuses to narrow the lists", async () => {
    server.use(
      http.put("*/api/v1/hooks/:name", () =>
        HttpResponse.json(
          {
            type: "about:blank",
            title: "Нельзя сузить список разрешённых возможностей",
            status: 409,
            detail: "order.flag",
          },
          { status: 409 },
        ),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(<HookForm hook={hookNamed("order.validate")} />);

    await user.click(screen.getByRole("button", { name: "Сохранить" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Нельзя сузить список разрешённых возможностей");
    expect(alert).toHaveTextContent("Убраны: order.flag");
  });
});
