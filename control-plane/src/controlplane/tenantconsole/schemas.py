from datetime import datetime
from typing import Any

from pydantic import BaseModel, Field


class HookView(BaseModel):
    name: str
    def_version: int
    input_schema: dict[str, Any]
    output_schema: dict[str, Any]
    timeout_ms: int = Field(gt=0)
    memory_max_pages: int = Field(gt=0)
    allowed_host_functions: list[str]
    allowed_effect_types: list[str]
    sample_input: Any = None


class TenantBinding(BaseModel):
    active_module_id: str | None
    active_module_hash: str | None
    config: dict[str, str]
    config_version: int
    updated_at: datetime


class TenantHook(BaseModel):
    hook: HookView
    binding: TenantBinding | None


class TenantHookList(BaseModel):
    items: list[TenantHook]
