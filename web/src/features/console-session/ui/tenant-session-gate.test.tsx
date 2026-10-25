import { useQuery } from "@tanstack/react-query";
import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";

import { hookQueries } from "@/entities/hook";
import { server } from "@/mocks/node";
import { clearToken, getToken, setToken } from "@/shared/api";
import { makeJwt, renderWithQuery } from "@/shared/lib/test-utils";

import { TenantSessionGate } from "./tenant-session-gate";
import { TenantName, TenantSessionInfo } from "./tenant-session-info";

const EXPIRED_TITLE = "Сессия консоли не начата или истекла";

function unauthorized() {
  return HttpResponse.json({ type: "about:blank", title: "Не авторизован", status: 401 }, { status: 401 });
}

function HookCount() {
  const { data } = useQuery(hookQueries.list());
  return <p>хуков: {data?.length ?? "…"}</p>;
}

afterEach(() => {
  vi.useRealTimers();
  window.history.replaceState(null, "", "/");
});

describe("TenantSessionGate", () => {
  it("keeps the token from the fragment and removes it from the address", async () => {
    clearToken("tenant");
    window.history.replaceState(null, "", "/tenant/hooks?tab=1#token=jwt-from-shop");

    renderWithQuery(<TenantSessionGate>консоль</TenantSessionGate>);

    expect(await screen.findByText("консоль")).toBeInTheDocument();
    expect(getToken("tenant")).toBe("jwt-from-shop");
    expect(window.location.hash).toBe("");
    expect(window.location.pathname).toBe("/tenant/hooks");
    expect(window.location.search).toBe("?tab=1");
  });

  it("replaces the current session with a token from a new fragment", async () => {
    renderWithQuery(<TenantSessionGate>консоль</TenantSessionGate>);
    await screen.findByText("консоль");

    act(() => {
      window.history.replaceState(null, "", "/tenant#token=next");
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });

    expect(getToken("tenant")).toBe("next");
    expect(window.location.hash).toBe("");
  });

  it("shows the expired session screen without a token", async () => {
    clearToken("tenant");
    renderWithQuery(<TenantSessionGate>консоль</TenantSessionGate>);

    expect(await screen.findByText(EXPIRED_TITLE)).toBeInTheDocument();
    expect(screen.getByText("Откройте консоль заново из магазина.")).toBeInTheDocument();
    expect(screen.queryByText("консоль")).not.toBeInTheDocument();
  });

  it("goes to the expired session screen on 401", async () => {
    server.use(http.get("*/api/v1/tenant/hooks", unauthorized));
    renderWithQuery(
      <TenantSessionGate>
        <HookCount />
      </TenantSessionGate>,
    );

    expect(await screen.findByText(EXPIRED_TITLE)).toBeInTheDocument();
    expect(getToken("tenant")).toBeNull();
  });

  it("drops an already expired token", async () => {
    setToken("tenant", makeJwt({ sub: "merchant-a", exp: Math.floor(Date.now() / 1000) - 1 }));
    renderWithQuery(<TenantSessionGate>консоль</TenantSessionGate>);

    expect(await screen.findByText(EXPIRED_TITLE)).toBeInTheDocument();
    expect(getToken("tenant")).toBeNull();
  });

  it("ends the session at exp without waiting for 401", async () => {
    vi.useFakeTimers();
    setToken("tenant", makeJwt({ sub: "merchant-a", exp: Math.floor(Date.now() / 1000) + 60 }));
    renderWithQuery(<TenantSessionGate>консоль</TenantSessionGate>);
    await act(async () => {});
    expect(screen.getByText("консоль")).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(61_000);
    });

    expect(screen.getByText(EXPIRED_TITLE)).toBeInTheDocument();
    expect(getToken("tenant")).toBeNull();
  });

  it("rejects a pasted token that has already expired", async () => {
    clearToken("tenant");
    const user = userEvent.setup();
    renderWithQuery(<TenantSessionGate>консоль</TenantSessionGate>);

    await user.type(
      await screen.findByLabelText("Токен консоли тенанта"),
      makeJwt({ sub: "merchant-a", exp: 1 }),
    );
    await user.click(screen.getByRole("button", { name: "Войти" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Срок действия токена истёк");
    expect(getToken("tenant")).toBeNull();
  });

  it("rejects a pasted token the server does not accept", async () => {
    server.use(http.get("*/api/v1/tenant/hooks", unauthorized));
    clearToken("tenant");
    const user = userEvent.setup();
    renderWithQuery(<TenantSessionGate>консоль</TenantSessionGate>);

    await user.type(await screen.findByLabelText("Токен консоли тенанта"), "forged");
    await user.click(screen.getByRole("button", { name: "Войти" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Токен не подошёл");
    expect(getToken("tenant")).toBeNull();
  });

  it("accepts a pasted token while the endpoint is not implemented yet", async () => {
    server.use(
      http.get("*/api/v1/tenant/hooks", () =>
        HttpResponse.json(
          { type: "about:blank", title: "Не реализовано", status: 501, detail: "T6" },
          { status: 501 },
        ),
      ),
    );
    clearToken("tenant");
    const user = userEvent.setup();
    renderWithQuery(<TenantSessionGate>консоль</TenantSessionGate>);

    await user.type(await screen.findByLabelText("Токен консоли тенанта"), "jwt");
    await user.click(screen.getByRole("button", { name: "Войти" }));

    expect(await screen.findByText("консоль")).toBeInTheDocument();
    expect(getToken("tenant")).toBe("jwt");
  });

  it("shows how to get a token in development", async () => {
    clearToken("tenant");
    renderWithQuery(<TenantSessionGate devHint>консоль</TenantSessionGate>);

    expect(await screen.findByText(/console-sessions/)).toBeInTheDocument();
  });
});

describe("TenantName and TenantSessionInfo", () => {
  it("show the tenant and when the session ends", () => {
    const exp = Date.UTC(2026, 9, 10, 12, 0, 0) / 1000;
    setToken("tenant", makeJwt({ sub: "merchant-b", exp }));
    renderWithQuery(
      <>
        <TenantName />
        <TenantSessionInfo />
      </>,
    );

    expect(screen.getByText("merchant-b")).toBeInTheDocument();
    expect(screen.getByText("Сессия до")).toBeInTheDocument();
  });

  it("use the mock tenant for the mock token", () => {
    setToken("tenant", "mock");
    renderWithQuery(
      <>
        <TenantName />
        <TenantSessionInfo />
      </>,
    );

    expect(screen.getByText("merchant-a")).toBeInTheDocument();
    expect(screen.queryByText("Сессия до")).not.toBeInTheDocument();
  });
});
