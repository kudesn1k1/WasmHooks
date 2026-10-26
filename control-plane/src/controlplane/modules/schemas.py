from datetime import datetime
from typing import Literal

from pydantic import BaseModel

from controlplane.dpclient.schemas import ValidationReport

ModuleStatus = Literal["uploaded", "validating", "validated", "rejected", "archived"]


class Module(BaseModel):
    id: str
    hook: str
    content_hash: str
    size_bytes: int
    status: ModuleStatus
    validation_report: ValidationReport | None
    uploaded_at: datetime


class ModuleList(BaseModel):
    items: list[Module]
