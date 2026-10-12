"""The configuration snapshot, field for field as in
api/dataplane-internal.openapi.yaml (and dataplane/internal/config.Snapshot)."""

from typing import Any

from pydantic import BaseModel


class APIKeyOut(BaseModel):
    id: str
    name: str
    sha256: str


class HookDefOut(BaseModel):
    name: str
    def_version: int
    input_schema: dict[str, Any]
    output_schema: dict[str, Any]
    timeout_ms: int
    memory_max_pages: int
    allowed_host_functions: list[str]
    allowed_effect_types: list[str]
    sample_input: dict[str, Any]


class TenantOut(BaseModel):
    external_id: str
    concurrency_limit: int
    rate_limit_rps: int


class BindingOut(BaseModel):
    tenant_id: str
    hook: str
    module_hash: str
    config: dict[str, str]
    config_version: int


class Snapshot(BaseModel):
    version: int
    api_keys: list[APIKeyOut]
    hooks: list[HookDefOut]
    tenants: list[TenantOut]
    bindings: list[BindingOut]
