import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";

import { server } from "@/mocks/node";
import { clearToken, getToken } from "@/shared/api";
import { renderWithQuery } from "@/shared/lib/test-utils";

import { OperatorSessionGate } from "./operator-session-gate";

describe("OperatorSessionGate", () => {
  it("warns that the key gives full access", () => {
    clearToken("operator");
    renderWithQuery(<OperatorSessionGate>консоль</OperatorSessionGate>);

    expect(screen.getByText(/полный доступ к установке/)).toBeInTheDocument();
  });

  it("checks the key before keeping it", async () => {
    clearToken("operator");
    const user = userEvent.setup();
    renderWithQuery(<OperatorSessionGate>консоль</OperatorSessionGate>);

    await user.type(screen.getByLabelText("Ключ установки"), "whk_good");
    await user.click(screen.getByRole("button", { name: "Войти" }));

    expect(await screen.findByText("консоль")).toBeInTheDocument();
    expect(getToken("operator")).toBe("whk_good");
  });

  it("says the key did not fit on 401 and does not keep it", async () => {
    server.use(
      http.get("*/api/v1/hooks", () =>
        HttpResponse.json({ type: "about:blank", title: "Не авторизован", status: 401 }, { status: 401 }),
      ),
    );
    clearToken("operator");
    const user = userEvent.setup();
    renderWithQuery(<OperatorSessionGate>консоль</OperatorSessionGate>);

    await user.type(screen.getByLabelText("Ключ установки"), "whk_bad");
    await user.click(screen.getByRole("button", { name: "Войти" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Ключ не подошёл");
    expect(getToken("operator")).toBeNull();
    expect(screen.queryByText("консоль")).not.toBeInTheDocument();
  });
});
