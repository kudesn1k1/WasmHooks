"""Module validation report, field for field as in
api/dataplane-internal.openapi.yaml (ValidationReport). The data plane
produces it; the control plane stores it and shows it in the tenant console."""

from typing import Literal

from pydantic import BaseModel, Field

CheckName = Literal["fetch", "compile", "exports", "imports", "sample_call"]


class ValidationCheck(BaseModel):
    name: CheckName
    ok: bool
    # Omitted for passed checks, as in the data plane's contract, rather than
    # sent as null: the console's schema treats it as an optional string.
    detail: str | None = Field(default=None, exclude_if=lambda v: v is None)


class ValidationReport(BaseModel):
    ok: bool
    checks: list[ValidationCheck]
