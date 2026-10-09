"""The snapshot endpoint answers exactly what api/dataplane-internal.openapi.yaml promises."""

from typing import Any

import httpx
import yaml
from jsonschema import Draft202012Validator
from sqlalchemy.ext.asyncio import AsyncEngine

from tests.factories import REPO_ROOT, seed_full


def snapshot_validator() -> Draft202012Validator:
    doc: dict[str, Any] = yaml.safe_load(
        (REPO_ROOT / "api" / "dataplane-internal.openapi.yaml").read_text(encoding="utf-8")
    )
    # OpenAPI 3.1 schemas are JSON Schema 2020-12. The document itself is the
    # root resource, so "#/components/schemas/..." refs resolve; its
    # non-schema keys (openapi, info, paths) are ignored by the validator.
    return Draft202012Validator({**doc, "$ref": "#/components/schemas/Snapshot"})


def test_contract_schema_is_strict_enough() -> None:
    # Guard against a validator that accepts anything.
    errors = list(snapshot_validator().iter_errors({"version": 1}))
    assert errors, "a snapshot without api_keys/hooks/tenants/bindings must be rejected"


async def test_empty_snapshot_matches_contract(client: httpx.AsyncClient) -> None:
    resp = await client.get(
        "/internal/v1/config/snapshot", headers={"Authorization": "Bearer test-internal-token"}
    )
    snapshot_validator().validate(resp.json())


async def test_full_snapshot_matches_contract(
    client: httpx.AsyncClient, engine: AsyncEngine
) -> None:
    await seed_full(engine)
    resp = await client.get(
        "/internal/v1/config/snapshot", headers={"Authorization": "Bearer test-internal-token"}
    )
    snapshot_validator().validate(resp.json())
