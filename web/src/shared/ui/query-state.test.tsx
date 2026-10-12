import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ApiError } from "@/shared/api";

import { ErrorState } from "./query-state";

describe("ErrorState", () => {
  it("shows 501 as not implemented yet, not as a failure", () => {
    render(<ErrorState error={new ApiError(501, "Ещё не реализовано", "T3")} />);

    expect(screen.getByRole("status")).toHaveTextContent("Ещё не реализовано на бэкенде: T3");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows other errors as failures", () => {
    render(<ErrorState error={new ApiError(500, "Внутренняя ошибка сервера")} />);

    expect(screen.getByRole("alert")).toHaveTextContent("Внутренняя ошибка сервера");
  });
});
