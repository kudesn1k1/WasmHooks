from datetime import datetime

from pydantic import BaseModel, Field


class TenantCreate(BaseModel):
    external_id: str = Field(min_length=1, max_length=128)
    name: str
    rate_limit_rps: int = Field(0, ge=0)
    concurrency_limit: int = Field(0, ge=0)


class TenantPatch(BaseModel):
    name: str | None = None
    rate_limit_rps: int | None = Field(None, ge=0)
    concurrency_limit: int | None = Field(None, ge=0)


class Tenant(TenantCreate):
    created_at: datetime
    updated_at: datetime


class TenantList(BaseModel):
    items: list[Tenant]
