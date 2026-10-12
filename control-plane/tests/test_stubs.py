from typing import Any

import httpx
import pytest
import pytest_asyncio
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.auth.keys import create_api_key
from controlplane.auth.tenant_tokens import TenantTokens
from controlplane.settings import Settings

WASM = {"file": ("m.wasm", b"\x00asm\x01\x00\x00\x00", "application/wasm")}

# (method, path, task, request kwargs, caller)
ENDPOINTS: list[tuple[str, str, str, dict[str, Any], str]] = [
    ("GET", "/api/v1/tenants", "T1", {}, "operator"),
    (
        "POST",
        "/api/v1/tenants",
        "T1",
        {"json": {"external_id": "merchant-a", "name": "A"}},
        "operator",
    ),
    ("GET", "/api/v1/tenants/merchant-a", "T1", {}, "operator"),
    ("PATCH", "/api/v1/tenants/merchant-a", "T1", {"json": {"name": "B"}}, "operator"),
    ("GET", "/api/v1/api-keys", "T2", {}, "operator"),
    ("POST", "/api/v1/api-keys", "T2", {"json": {"name": "ci"}}, "operator"),
    ("DELETE", "/api/v1/api-keys/abc", "T2", {}, "operator"),
    ("GET", "/api/v1/tenant/hooks", "T6", {}, "tenant"),
    ("GET", "/api/v1/tenant/hooks/order.validate", "T6", {}, "tenant"),
    ("GET", "/api/v1/tenant/hooks/order.validate/modules", "T3", {}, "tenant"),
    ("POST", "/api/v1/tenant/hooks/order.validate/modules", "T3", {"files": WASM}, "tenant"),
    (
        "PUT",
        "/api/v1/tenant/hooks/order.validate/binding",
        "T5",
        {"json": {"active_module_id": "m1"}},
        "tenant",
    ),
]
TENANT_ENDPOINTS = [e for e in ENDPOINTS if e[4] == "tenant"]


def _id(e: tuple[str, str, str, dict[str, Any], str]) -> str:
    return f"{e[0]} {e[1]}"


@pytest_asyncio.fixture
async def operator_key(engine: AsyncEngine) -> str:
    async with engine.begin() as conn:
        return (await create_api_key(conn, "t")).token


@pytest.fixture
def tenant_token(settings: Settings) -> str:
    tokens = TenantTokens(
        settings.tenant_jwt_secret.get_secret_value(), settings.tenant_token_ttl_s
    )
    return tokens.issue("merchant-a").token


@pytest.mark.parametrize("endpoint", ENDPOINTS, ids=_id)
async def test_no_credentials_is_401(
    client: httpx.AsyncClient, endpoint: tuple[str, str, str, dict[str, Any], str]
) -> None:
    method, path, _, kwargs, _ = endpoint
    resp = await client.request(method, path, **kwargs)
    assert resp.status_code == 401


@pytest.mark.parametrize("endpoint", ENDPOINTS, ids=_id)
async def test_valid_credentials_reach_the_stub(
    client: httpx.AsyncClient,
    operator_key: str,
    tenant_token: str,
    endpoint: tuple[str, str, str, dict[str, Any], str],
) -> None:
    method, path, task, kwargs, caller = endpoint
    token = operator_key if caller == "operator" else tenant_token
    resp = await client.request(
        method, path, headers={"Authorization": f"Bearer {token}"}, **kwargs
    )
    assert resp.status_code == 501
    assert resp.headers["content-type"] == "application/problem+json"
    assert resp.json()["detail"] == task


@pytest.mark.parametrize("endpoint", TENANT_ENDPOINTS, ids=_id)
async def test_operator_key_is_not_a_tenant_token(
    client: httpx.AsyncClient,
    operator_key: str,
    endpoint: tuple[str, str, str, dict[str, Any], str],
) -> None:
    method, path, _, kwargs, _ = endpoint
    resp = await client.request(
        method, path, headers={"Authorization": f"Bearer {operator_key}"}, **kwargs
    )
    assert resp.status_code == 401
