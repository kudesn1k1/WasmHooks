import { render, screen } from "@testing-library/react";
import { renderToString } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { MockProvider } from "./mock-provider";

const { createWorker, start } = vi.hoisted(() => {
  const start = vi.fn().mockResolvedValue(undefined);
  return { start, createWorker: vi.fn(() => ({ start })) };
});

vi.mock("@/mocks/browser", () => ({ createWorker }));

describe("MockProvider", () => {
  it("hydrates server content without starting MSW when mocks are disabled", () => {
    const ui = <MockProvider mode="disabled"><p>Консоль тенанта</p></MockProvider>;
    const container = document.createElement("div");
    container.innerHTML = renderToString(ui);
    const onRecoverableError = vi.fn();

    render(ui, { container, hydrate: true, onRecoverableError });

    expect(container).toHaveTextContent("Консоль тенанта");
    expect(onRecoverableError).not.toHaveBeenCalled();
    expect(start).not.toHaveBeenCalled();
  });

  it("hydrates the loading state and renders children after MSW starts", async () => {
    const ui = <MockProvider mode="all"><p>Консоль тенанта</p></MockProvider>;
    const container = document.createElement("div");
    document.body.append(container);
    container.innerHTML = renderToString(ui);
    const onRecoverableError = vi.fn();

    expect(container).toHaveTextContent("Запуск моков API…");
    render(ui, { container, hydrate: true, onRecoverableError });

    expect(await screen.findByText("Консоль тенанта")).toBeInTheDocument();
    expect(createWorker).toHaveBeenCalledWith("all");
    expect(onRecoverableError).not.toHaveBeenCalled();
  });
});
