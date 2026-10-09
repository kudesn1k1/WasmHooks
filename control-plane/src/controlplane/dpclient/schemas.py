"""Module validation report, field for field as in
api/dataplane-internal.openapi.yaml (ValidationReport). The data plane
produces it; the control plane stores it and shows it in the tenant console."""

from typing import Literal

from pydantic import BaseModel

CheckName = Literal["fetch", "compile", "exports", "imports", "sample_call"]


class ValidationCheck(BaseModel):
    name: CheckName
    ok: bool
    detail: str | None = None


class ValidationReport(BaseModel):
    ok: bool
    checks: list[ValidationCheck]
