"""Request and response shapes of the operator hooks API."""

from datetime import datetime
from typing import Any

from pydantic import BaseModel, ConfigDict, Field

# Same rule as the database constraint in tables.py: dot-separated
# lowercase segments, e.g. "checkout.discount".
HOOK_NAME_PATTERN = r"^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$"


class HookSpec(BaseModel):
    """Everything about a hook except its name: the body of PUT."""

    model_config = ConfigDict(extra="forbid")

    input_schema: dict[str, Any] = Field(
        description="JSON Schema (draft 2020-12) of the payload operators send."
    )
    output_schema: dict[str, Any] = Field(
        description="JSON Schema (draft 2020-12) of the result scripts return."
    )
    timeout_ms: int = Field(ge=1, le=30000, description="Time limit of one call.")
    memory_max_pages: int = Field(
        ge=16, le=16384, description="Linear memory limit in 64 KiB pages."
    )
    allowed_host_functions: list[str] = Field(
        default_factory=list, description="Host functions scripts may import. Only grows."
    )
    allowed_effect_types: list[str] = Field(
        default_factory=list, description="Effect types scripts may return. Only grows."
    )
    sample_input: dict[str, Any] = Field(
        description="A payload matching input_schema, used to validate uploaded scripts."
    )


class HookCreate(HookSpec):
    """The body of POST: a spec plus the hook's name."""

    name: str = Field(pattern=HOOK_NAME_PATTERN, max_length=128)


class Hook(HookCreate):
    def_version: int = Field(description="Grows by one on every change of the definition.")
    created_at: datetime
    updated_at: datetime


class HookList(BaseModel):
    items: list[Hook]
