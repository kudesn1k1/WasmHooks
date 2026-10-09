from datetime import datetime

from pydantic import BaseModel, Field


class ApiKeyCreate(BaseModel):
    name: str = Field(min_length=1, max_length=64)


class ApiKey(BaseModel):
    id: str
    name: str
    created_at: datetime
    revoked_at: datetime | None


class ApiKeyCreated(ApiKey):
    token: str  # shown once, at creation


class ApiKeyList(BaseModel):
    items: list[ApiKey]
