"""Direct inserts for tests that need data the API cannot create yet.

Each helper writes rows only; callers decide whether to bump the snapshot
version (most snapshot tests do, through bump()).
"""

import hashlib
import uuid
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from sqlalchemy import insert, update
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.tables import api_keys, bindings, hooks, modules, tenants

REPO_ROOT = Path(__file__).resolve().parents[2]
FIXTURES = REPO_ROOT / "dataplane" / "testdata" / "wasm"

INPUT_SCHEMA: dict[str, Any] = {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "required": ["cart_total", "customer"],
    "properties": {
        "cart_total": {"type": "number"},
        "customer": {
            "type": "object",
            "required": ["id", "lifetime_spend"],
            "properties": {
                "id": {"type": "string"},
                "lifetime_spend": {"type": "number"},
            },
        },
    },
}
OUTPUT_SCHEMA: dict[str, Any] = {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "required": ["discount_percent", "reason"],
    "properties": {
        "discount_percent": {"type": "integer", "minimum": 0, "maximum": 100},
        "reason": {"type": "string"},
    },
}
SAMPLE_INPUT: dict[str, Any] = {
    "cart_total": 120.5,
    "customer": {"id": "c-1", "lifetime_spend": 1500},
}


def wasm_hash(name: str) -> str:
    return "sha256:" + hashlib.sha256((FIXTURES / name).read_bytes()).hexdigest()


async def add_api_key(
    conn: AsyncConnection, key_id: str, name: str, token: str, *, revoked: bool = False
) -> None:
    await conn.execute(
        insert(api_keys).values(
            id=key_id,
            name=name,
            sha256=hashlib.sha256(token.encode()).hexdigest(),
            revoked_at=datetime.now(UTC) if revoked else None,
        )
    )


async def add_hook(
    conn: AsyncConnection, name: str = "checkout.discount", **overrides: Any
) -> uuid.UUID:
    values: dict[str, Any] = {
        "name": name,
        "def_version": 1,
        "input_schema": INPUT_SCHEMA,
        "output_schema": OUTPUT_SCHEMA,
        "timeout_ms": 50,
        "memory_max_pages": 64,
        "allowed_host_functions": [],
        "allowed_effect_types": [],
        "sample_input": SAMPLE_INPUT,
    } | overrides
    result = await conn.execute(insert(hooks).values(**values).returning(hooks.c.id))
    return uuid.UUID(str(result.scalar_one()))


async def add_tenant(
    conn: AsyncConnection, external_id: str, *, concurrency_limit: int = 0, rate_limit_rps: int = 0
) -> uuid.UUID:
    result = await conn.execute(
        insert(tenants)
        .values(
            external_id=external_id,
            name=external_id,
            concurrency_limit=concurrency_limit,
            rate_limit_rps=rate_limit_rps,
        )
        .returning(tenants.c.id)
    )
    return uuid.UUID(str(result.scalar_one()))


async def add_module(
    conn: AsyncConnection,
    tenant_id: uuid.UUID,
    hook_id: uuid.UUID,
    content_hash: str,
    *,
    status: str = "validated",
) -> uuid.UUID:
    result = await conn.execute(
        insert(modules)
        .values(
            tenant_id=tenant_id,
            hook_id=hook_id,
            content_hash=content_hash,
            size_bytes=1024,
            status=status,
        )
        .returning(modules.c.id)
    )
    return uuid.UUID(str(result.scalar_one()))


async def add_binding(
    conn: AsyncConnection,
    tenant_id: uuid.UUID,
    hook_id: uuid.UUID,
    module_id: uuid.UUID | None,
    config: dict[str, str] | None = None,
) -> None:
    await conn.execute(
        insert(bindings).values(
            tenant_id=tenant_id,
            hook_id=hook_id,
            active_module_id=module_id,
            config=config or {},
        )
    )


async def revoke_key(conn: AsyncConnection, key_id: str) -> None:
    await conn.execute(
        update(api_keys).where(api_keys.c.id == key_id).values(revoked_at=datetime.now(UTC))
    )
