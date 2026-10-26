import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { setToken } from "@/shared/api";
import { makeJwt, renderWithQuery } from "@/shared/lib/test-utils";

import { TenantHooksPage } from "./tenant-hooks-page";

describe("TenantHooksPage", () => {
  it("lists hooks with their active version and links to them", async () => {
    setToken("tenant", makeJwt({ sub: "merchant-a" }));
    renderWithQuery(<TenantHooksPage />);

    expect(await screen.findByText("Активная версия: 9b7d04f19b7d")).toBeInTheDocument();
    expect(screen.getByText("Активной версии нет: хук работает без обработчика")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Открыть checkout.discount" })).toHaveAttribute(
      "href",
      "/tenant/hooks/checkout.discount",
    );
    expect(screen.getByRole("navigation", { name: "Навигация по разделам" })).toHaveTextContent("merchant-a");
  });
});
