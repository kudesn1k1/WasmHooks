import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { clearToken, getToken } from "@/shared/api";

import { SessionGate } from "./session-gate";

const gate = (
  <SessionGate kind="tenant" title="Сессия не начата" description="" label="Токен">
    <p>консоль</p>
  </SessionGate>
);

describe("SessionGate", () => {
  it("shows the console when there is a token", () => {
    render(gate);

    expect(screen.getByText("консоль")).toBeInTheDocument();
  });

  it("switches to the sign-in screen when the token is cleared", () => {
    render(gate);

    act(() => clearToken("tenant"));

    expect(screen.queryByText("консоль")).not.toBeInTheDocument();
    expect(screen.getByText("Сессия не начата")).toBeInTheDocument();
  });

  it("stores a pasted token and opens the console", async () => {
    clearToken("tenant");
    const user = userEvent.setup();
    render(gate);

    await user.type(screen.getByLabelText("Токен"), "  jwt-pasted  ");
    await user.click(screen.getByRole("button", { name: "Войти" }));

    expect(getToken("tenant")).toBe("jwt-pasted");
    expect(screen.getByText("консоль")).toBeInTheDocument();
  });
});
