"""Database constraints mirror the data plane's snapshot checks (decision D22):
whatever the API or a hand-written query does, the database refuses rows that
would make a snapshot the data plane rejects."""

import uuid
from typing import Any

import pytest
from alembic.autogenerate import compare_metadata
from alembic.migration import MigrationContext
from sqlalchemy import Connection, select, text
from sqlalchemy.exc import DBAPIError
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.tables import config_state, metadata
from tests.factories import add_binding, add_hook, add_module, add_tenant, wasm_hash

HASH = wasm_hash("discount.wasm")


async def expect_rejected(engine: AsyncEngine, sql: str, **params: Any) -> None:
    with pytest.raises(DBAPIError):
        async with engine.begin() as conn:
            await conn.execute(text(sql), params)


async def expect_accepted(engine: AsyncEngine, sql: str, **params: Any) -> None:
    async with engine.begin() as conn:
        await conn.execute(text(sql), params)


# config_state


async def test_config_state_starts_with_one_row_at_version_1(engine: AsyncEngine) -> None:
    async with engine.connect() as conn:
        rows = (await conn.execute(select(config_state))).all()
    assert [tuple(r) for r in rows] == [(1, 1)]


async def test_config_state_has_a_single_row(engine: AsyncEngine) -> None:
    await expect_rejected(engine, "INSERT INTO config_state (id, version) VALUES (2, 1)")


async def test_config_state_version_is_positive(engine: AsyncEngine) -> None:
    await expect_rejected(engine, "UPDATE config_state SET version = 0")


# api_keys

KEY_SQL = "INSERT INTO api_keys (id, name, sha256) VALUES (:id, :name, :sha)"
GOOD_SHA = "a" * 64


@pytest.mark.parametrize("sha", ["A" * 64, "a" * 63, "g" * 64])
async def test_api_key_hash_format(engine: AsyncEngine, sha: str) -> None:
    await expect_rejected(engine, KEY_SQL, id="k1", name="n", sha=sha)


async def test_api_key_name_unique(engine: AsyncEngine) -> None:
    await expect_accepted(engine, KEY_SQL, id="k1", name="n", sha=GOOD_SHA)
    await expect_rejected(engine, KEY_SQL, id="k2", name="n", sha="b" * 64)


# tenants

TENANT_SQL = "INSERT INTO tenants (external_id, name, concurrency_limit) VALUES (:ext, 'x', :limit)"


async def test_tenant_external_id_not_empty(engine: AsyncEngine) -> None:
    await expect_rejected(engine, TENANT_SQL, ext="", limit=0)


async def test_tenant_external_id_unique(engine: AsyncEngine) -> None:
    await expect_accepted(engine, TENANT_SQL, ext="m", limit=0)
    await expect_rejected(engine, TENANT_SQL, ext="m", limit=0)


async def test_tenant_limits_non_negative(engine: AsyncEngine) -> None:
    await expect_rejected(engine, TENANT_SQL, ext="m", limit=-1)


# hooks


async def insert_hook(engine: AsyncEngine, **overrides: Any) -> None:
    async with engine.begin() as conn:
        await add_hook(conn, **overrides)


@pytest.mark.parametrize(
    "name", ["Checkout.Discount", "checkout..discount", "a" * 129, ".a", "a-b"]
)
async def test_hook_name_rejected(engine: AsyncEngine, name: str) -> None:
    with pytest.raises(DBAPIError):
        await insert_hook(engine, name=name)


@pytest.mark.parametrize("name", ["checkout.discount", "order_validate", "a.b2.c_3"])
async def test_hook_name_accepted(engine: AsyncEngine, name: str) -> None:
    await insert_hook(engine, name=name)


@pytest.mark.parametrize(
    "overrides",
    [
        {"timeout_ms": 0},
        {"timeout_ms": 30001},
        {"memory_max_pages": 15},
        {"memory_max_pages": 16385},
        {"def_version": 0},
        {"input_schema": []},
        {"output_schema": "x"},
        {"sample_input": "x"},
    ],
)
async def test_hook_values_rejected(engine: AsyncEngine, overrides: dict[str, Any]) -> None:
    with pytest.raises(DBAPIError):
        await insert_hook(engine, **overrides)


async def test_hook_bounds_accepted(engine: AsyncEngine) -> None:
    await insert_hook(engine, name="a", timeout_ms=1, memory_max_pages=16)
    await insert_hook(engine, name="b", timeout_ms=30000, memory_max_pages=16384)


# modules and bindings


async def tenant_and_hook(
    engine: AsyncEngine, ext: str = "m", hook: str = "h"
) -> tuple[uuid.UUID, uuid.UUID]:
    async with engine.begin() as conn:
        return await add_tenant(conn, ext), await add_hook(conn, hook)


async def test_module_hash_format(engine: AsyncEngine) -> None:
    tenant, hook = await tenant_and_hook(engine)
    with pytest.raises(DBAPIError):
        async with engine.begin() as conn:
            await add_module(conn, tenant, hook, "sha256:ABC")


async def test_module_status_enum(engine: AsyncEngine) -> None:
    tenant, hook = await tenant_and_hook(engine)
    with pytest.raises(DBAPIError):
        async with engine.begin() as conn:
            await add_module(conn, tenant, hook, HASH, status="active")


async def test_module_unique_per_tenant_hook_hash(engine: AsyncEngine) -> None:
    tenant, hook = await tenant_and_hook(engine)
    async with engine.begin() as conn:
        await add_module(conn, tenant, hook, HASH)
    with pytest.raises(DBAPIError):
        async with engine.begin() as conn:
            await add_module(conn, tenant, hook, HASH)


async def test_binding_cannot_activate_another_tenants_module(engine: AsyncEngine) -> None:
    tenant_a, hook = await tenant_and_hook(engine, "a", "h")
    async with engine.begin() as conn:
        tenant_b = await add_tenant(conn, "b")
        module_b = await add_module(conn, tenant_b, hook, HASH)
    with pytest.raises(DBAPIError):
        async with engine.begin() as conn:
            await add_binding(conn, tenant_a, hook, module_b)


async def test_binding_cannot_activate_module_of_another_hook(engine: AsyncEngine) -> None:
    tenant, hook_1 = await tenant_and_hook(engine, "a", "h1")
    async with engine.begin() as conn:
        hook_2 = await add_hook(conn, "h2")
        module_2 = await add_module(conn, tenant, hook_2, HASH)
    with pytest.raises(DBAPIError):
        async with engine.begin() as conn:
            await add_binding(conn, tenant, hook_1, module_2)


async def test_binding_without_active_module(engine: AsyncEngine) -> None:
    tenant, hook = await tenant_and_hook(engine)
    async with engine.begin() as conn:
        await add_binding(conn, tenant, hook, None)


@pytest.mark.parametrize("config", ['{"a": 1}', "[]", '{"a": null}', '{"a": {"b": "c"}}'])
async def test_binding_config_must_be_object_of_strings(engine: AsyncEngine, config: str) -> None:
    tenant, hook = await tenant_and_hook(engine)
    await expect_rejected(
        engine,
        "INSERT INTO bindings (tenant_id, hook_id, config) VALUES (:t, :h, CAST(:c AS jsonb))",
        t=tenant,
        h=hook,
        c=config,
    )


@pytest.mark.parametrize("config", ['{"a": "1"}', "{}"])
async def test_binding_config_strings_accepted(engine: AsyncEngine, config: str) -> None:
    tenant, hook = await tenant_and_hook(engine)
    await expect_accepted(
        engine,
        "INSERT INTO bindings (tenant_id, hook_id, config) VALUES (:t, :h, CAST(:c AS jsonb))",
        t=tenant,
        h=hook,
        c=config,
    )


# migration vs tables.py

STRUCTURAL = {
    "add_table",
    "remove_table",
    "add_column",
    "remove_column",
    "add_constraint",
    "remove_constraint",
    "add_fk",
    "remove_fk",
    "add_index",
    "remove_index",
}


def _not_alembic_version(name: str | None, type_: str, parent_names: Any) -> bool:
    return not (type_ == "table" and name == "alembic_version")


def _structural_diffs(sync_conn: Connection) -> list[Any]:
    ctx = MigrationContext.configure(sync_conn, opts={"include_name": _not_alembic_version})
    diffs = compare_metadata(ctx, metadata)
    flat: list[Any] = []
    for d in diffs:
        flat.extend(d if isinstance(d, list) else [d])
    return [d for d in flat if isinstance(d, tuple) and d[0] in STRUCTURAL]


async def test_migration_matches_tables(engine: AsyncEngine) -> None:
    # Tables, columns, uniques and FKs. Autogenerate does not compare CHECK
    # constraints (proven by the tests above) and is noisy on ARRAY/JSONB types.
    async with engine.connect() as conn:
        diffs = await conn.run_sync(_structural_diffs)
    assert diffs == []
