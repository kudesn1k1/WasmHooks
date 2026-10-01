import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ConsoleShell } from "./console-shell";

const pathname = vi.hoisted(() => ({ current: "/" }));

vi.mock("next/navigation", () => ({
  usePathname: () => pathname.current,
}));

const nav = [
  { href: "/tenant/hooks", label: "Хуки" },
  { href: "/tenant/invocations", label: "Лог вызовов" },
];

describe("ConsoleShell", () => {
  beforeEach(() => {
    pathname.current = "/";
  });

  it("renders the title, navigation and content", () => {
    render(
      <ConsoleShell title="Консоль тенанта" nav={nav}>
        <p>контент</p>
      </ConsoleShell>,
    );

    expect(screen.getByText("Консоль тенанта")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Хуки" })).toHaveAttribute("href", "/tenant/hooks");
    expect(screen.getByText("контент")).toBeInTheDocument();
  });

  it("marks the current section, including nested pages", () => {
    pathname.current = "/tenant/hooks/checkout.discount";
    render(
      <ConsoleShell title="Консоль тенанта" nav={nav}>
        {null}
      </ConsoleShell>,
    );

    expect(screen.getByRole("link", { name: "Хуки" })).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("link", { name: "Лог вызовов" })).not.toHaveAttribute("aria-current");
  });

  it("does not treat a shared prefix as the same section", () => {
    pathname.current = "/tenant/hooks-archive";
    render(
      <ConsoleShell title="Консоль тенанта" nav={nav}>
        {null}
      </ConsoleShell>,
    );

    expect(screen.getByRole("link", { name: "Хуки" })).not.toHaveAttribute("aria-current");
  });
});
