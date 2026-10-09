import asyncio
from collections.abc import AsyncIterator
from typing import Any

import httpx
import pytest
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.auth.keys import create_api_key
from controlplane.tables import config_changes, config_state
from tests.factories import INPUT_SCHEMA, OUTPUT_SCHEMA, SAMPLE_INPUT

URL = "/api/v1/hooks"


@pytest.fixture
async def auth(engine: AsyncEngine) -> AsyncIterator[dict[str, str]]:
    async with engine.begin() as conn:
        key = await create_api_key(conn, "operator")
    yield {"Authorization": f"Bearer {key.token}"}


def spec(**overrides: Any) -> dict[str, Any]:
    return {
        "input_schema": INPUT_SCHEMA,
        "output_schema": OUTPUT_SCHEMA,
        "timeout_ms": 50,
        "memory_max_pages": 64,
        "allowed_host_functions": [],
        "allowed_effect_types": [],
        "sample_input": SAMPLE_INPUT,
    } | overrides


def hook(name: str = "checkout.discount", **overrides: Any) -> dict[str, Any]:
    return {"name": name} | spec(**overrides)


async def snapshot_version(engine: AsyncEngine) -> int:
    async with engine.connect() as conn:
        return int((await conn.execute(select(config_state.c.version))).scalar_one())


async def snapshot(client: httpx.AsyncClient) -> dict[str, Any]:
    resp = await client.get(
        "/internal/v1/config/snapshot", headers={"Authorization": "Bearer test-internal-token"}
    )
    body: dict[str, Any] = resp.json()
    return body


async def test_create_hook(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    before = await snapshot_version(engine)
    resp = await client.post(URL, json=hook(), headers=auth)
    assert resp.status_code == 201, resp.text
    created = resp.json()
    assert created["name"] == "checkout.discount"
    assert created["def_version"] == 1
    assert await snapshot_version(engine) == before + 1

    assert (await client.get(f"{URL}/checkout.discount", headers=auth)).json() == created
    assert (await client.get(URL, headers=auth)).json() == {"items": [created]}

    snap = await snapshot(client)
    assert [h["name"] for h in snap["hooks"]] == ["checkout.discount"]
    assert snap["hooks"][0]["def_version"] == 1


async def test_duplicate_name_is_409(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    assert (await client.post(URL, json=hook(), headers=auth)).status_code == 201
    before = await snapshot_version(engine)
    resp = await client.post(URL, json=hook(), headers=auth)
    assert resp.status_code == 409
    assert resp.headers["content-type"].startswith("application/problem+json")
    assert await snapshot_version(engine) == before


@pytest.mark.parametrize(
    "body",
    [
        hook(name="Checkout"),
        hook(timeout_ms=0),
        hook(memory_max_pages=15),
        hook() | {"unexpected": True},
        {k: v for k, v in hook().items() if k != "sample_input"},
    ],
    ids=["bad-name", "timeout-0", "pages-15", "extra-field", "no-sample-input"],
)
async def test_invalid_body_is_422(
    client: httpx.AsyncClient, auth: dict[str, str], body: dict[str, Any]
) -> None:
    resp = await client.post(URL, json=body, headers=auth)
    assert resp.status_code == 422
    assert resp.headers["content-type"].startswith("application/problem+json")


async def test_invalid_schema_is_422(client: httpx.AsyncClient, auth: dict[str, str]) -> None:
    resp = await client.post(URL, json=hook(input_schema={"type": 5}), headers=auth)
    assert resp.status_code == 422
    assert resp.json()["detail"].startswith("input_schema:")


async def test_remote_ref_is_rejected(client: httpx.AsyncClient, auth: dict[str, str]) -> None:
    # The data plane compiles schemas without network access: a remote $ref
    # would break every call of the hook there.
    remote = {"$ref": "https://example.com/schema.json"}
    resp = await client.post(URL, json=hook(output_schema=remote, sample_input={}), headers=auth)
    assert resp.status_code == 422
    assert "only local $ref" in resp.json()["detail"]


async def test_local_ref_is_accepted(client: httpx.AsyncClient, auth: dict[str, str]) -> None:
    local = {"$defs": {"obj": {"type": "object"}}, "$ref": "#/$defs/obj"}
    resp = await client.post(URL, json=hook(output_schema=local), headers=auth)
    assert resp.status_code == 201, resp.text


async def test_sample_input_must_match_input_schema(
    client: httpx.AsyncClient, auth: dict[str, str]
) -> None:
    resp = await client.post(URL, json=hook(sample_input={"cart_total": "x"}), headers=auth)
    assert resp.status_code == 422
    assert resp.json()["detail"].startswith("sample_input:")


async def test_put_without_changes_keeps_versions(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    await client.post(URL, json=hook(), headers=auth)
    before = await snapshot_version(engine)
    resp = await client.put(f"{URL}/checkout.discount", json=spec(), headers=auth)
    assert resp.status_code == 200
    assert resp.json()["def_version"] == 1
    assert await snapshot_version(engine) == before


async def test_put_with_change_bumps_versions(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    created = (await client.post(URL, json=hook(), headers=auth)).json()
    before = await snapshot_version(engine)
    resp = await client.put(f"{URL}/checkout.discount", json=spec(timeout_ms=100), headers=auth)
    assert resp.status_code == 200
    updated = resp.json()
    assert updated["def_version"] == 2
    assert updated["timeout_ms"] == 100
    assert updated["updated_at"] > created["updated_at"]
    assert await snapshot_version(engine) == before + 1
    assert (await snapshot(client))["hooks"][0]["def_version"] == 2


async def test_allowed_lists_only_grow(client: httpx.AsyncClient, auth: dict[str, str]) -> None:
    await client.post(URL, json=hook(allowed_effect_types=["email"]), headers=auth)
    narrowed = await client.put(
        f"{URL}/checkout.discount", json=spec(allowed_effect_types=[]), headers=auth
    )
    assert narrowed.status_code == 409
    assert narrowed.json()["detail"] == "email"
    grown = await client.put(
        f"{URL}/checkout.discount", json=spec(allowed_effect_types=["email", "sms"]), headers=auth
    )
    assert grown.status_code == 200


async def test_unknown_hook_is_404(client: httpx.AsyncClient, auth: dict[str, str]) -> None:
    assert (await client.get(f"{URL}/nope", headers=auth)).status_code == 404
    assert (await client.put(f"{URL}/nope", json=spec(), headers=auth)).status_code == 404


async def test_requires_operator_key(client: httpx.AsyncClient) -> None:
    assert (await client.get(URL)).status_code == 401
    assert (await client.post(URL, json=hook())).status_code == 401


async def test_allowed_lists_are_normalized(
    client: httpx.AsyncClient, auth: dict[str, str]
) -> None:
    resp = await client.post(URL, json=hook(allowed_host_functions=["b", "a", "a"]), headers=auth)
    assert resp.json()["allowed_host_functions"] == ["a", "b"]


async def test_concurrent_puts_lose_no_change(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    await client.post(URL, json=hook(), headers=auth)
    first, second = await asyncio.gather(
        client.put(f"{URL}/checkout.discount", json=spec(timeout_ms=100), headers=auth),
        client.put(f"{URL}/checkout.discount", json=spec(timeout_ms=200), headers=auth),
    )
    assert first.status_code == second.status_code == 200
    assert sorted([first.json()["def_version"], second.json()["def_version"]]) == [2, 3]

    async with engine.connect() as conn:
        rows = (
            (
                await conn.execute(
                    select(config_changes.c.payload)
                    .where(config_changes.c.kind == "hook.updated")
                    .order_by(config_changes.c.version)
                )
            )
            .scalars()
            .all()
        )
    assert [r["def_version"] for r in rows] == [2, 3]


# Review 2, I3: schemas Python accepts but the data plane (Go, RE2) cannot compile.
@pytest.mark.parametrize(
    ("field", "schema", "message"),
    [
        (
            "input_schema",
            {"type": "object", "properties": {"a": {"type": "string", "pattern": "^(?=a)"}}},
            "RE2",
        ),
        (
            "output_schema",
            {"type": "object", "properties": {"a": {"type": "string", "pattern": r"(a)\1"}}},
            "RE2",
        ),
        ("input_schema", {"type": "object", "patternProperties": {"^(?!x)": {}}}, "RE2"),
        (
            "output_schema",
            {"$schema": "https://example.com/my-meta", "type": "object"},
            "draft 2020-12",
        ),
        (
            "output_schema",
            {"type": "object", "properties": {"a": {"$ref": "#/$defs/nope"}}},
            "points to nothing",
        ),
        ("output_schema", {"$defs": {"a": {"$anchor": "x"}}, "type": "object"}, "$anchor"),
        (
            "output_schema",
            {"type": "object", "properties": {"a": {"$ref": "#x"}}},
            "only local $ref",
        ),
        (
            "input_schema",
            {"type": "object", "properties": {"a": {"type": "string", "pattern": "("}}},
            "regex",
        ),
    ],
    ids=[
        "lookahead",
        "backreference",
        "pattern-properties",
        "foreign-dialect",
        "dangling-ref",
        "anchor",
        "anchor-ref",
        "broken-regex",
    ],
)
async def test_schemas_the_data_plane_cannot_compile_are_422(
    client: httpx.AsyncClient,
    auth: dict[str, str],
    field: str,
    schema: dict[str, Any],
    message: str,
) -> None:
    body = hook(**{field: schema})
    if field == "input_schema":
        body["sample_input"] = {}
    resp = await client.post(URL, json=body, headers=auth)
    assert resp.status_code == 422, resp.text
    assert resp.json()["detail"].startswith(f"{field}:")
    assert message in resp.json()["detail"]


async def test_supported_schema_features_are_accepted(
    client: httpx.AsyncClient, auth: dict[str, str]
) -> None:
    out = {
        "$schema": "https://json-schema.org/draft/2020-12/schema",
        "$defs": {"pct": {"type": "integer", "minimum": 0, "maximum": 100}},
        "type": "object",
        "properties": {
            "discount_percent": {"$ref": "#/$defs/pct"},
            "reason": {"type": "string", "pattern": "^[a-z]+(?P<tail>[0-9]*)$"},
        },
        "examples": [{"pattern": "(?=data, not a schema)"}],
    }
    resp = await client.post(URL, json=hook(output_schema=out), headers=auth)
    assert resp.status_code == 201, resp.text


# Review 2, m4: values PostgreSQL or JSON cannot store are 422, not 500.
@pytest.mark.parametrize(
    "raw",
    [
        '"sample_input": {"cart_total": 1e400, "customer": {"id": "c", "lifetime_spend": 1}}',
        '"sample_input": {"cart_total": 1, "customer": {"id": "a\u0000b", "lifetime_spend": 1}}',
    ],
    ids=["infinite-number", "nul-character"],
)
async def test_unstorable_values_are_422(
    client: httpx.AsyncClient, auth: dict[str, str], raw: str
) -> None:
    import json

    base = json.dumps({k: v for k, v in hook().items() if k != "sample_input"})
    body = base[:-1] + ", " + raw + "}"
    resp = await client.post(URL, content=body, headers=auth | {"Content-Type": "application/json"})
    assert resp.status_code == 422, resp.text
