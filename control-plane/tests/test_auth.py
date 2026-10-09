import re
from datetime import UTC, datetime, timedelta
from typing import Annotated

import httpx
import jwt
import pytest
from asgi_lifespan import LifespanManager
from fastapi import Depends, FastAPI
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.auth.deps import TenantAuth, TenantPrincipal
from controlplane.auth.keys import create_api_key, generate_api_key, hash_token
from controlplane.auth.tenant_tokens import TenantTokens
from controlplane.cli import main
from controlplane.errors import ProblemError, install_error_handlers
from controlplane.tables import api_keys, config_state
from tests.factories import add_tenant, revoke_key

SECRET = "test-tenant-jwt-secret-0123456789abcdef"


# API keys


def test_generated_key_shape() -> None:
    key = generate_api_key("shop")
    assert re.fullmatch(r"whk_[0-9A-Za-z]{32}", key.token)
    assert re.fullmatch(r"key_[0-9a-f]{16}", key.id)
    assert key.sha256 == hash_token(key.token)
    assert re.fullmatch(r"[0-9a-f]{64}", key.sha256)
    assert generate_api_key("shop").token != key.token


async def test_create_api_key_bumps_version(engine: AsyncEngine) -> None:
    async with engine.begin() as conn:
        key = await create_api_key(conn, "shop")
    async with engine.connect() as conn:
        assert (await conn.execute(select(config_state.c.version))).scalar_one() == 2
        stored = (await conn.execute(select(api_keys.c.sha256))).scalar_one()
    assert stored == key.sha256


async def test_new_key_appears_in_snapshot(client: httpx.AsyncClient, engine: AsyncEngine) -> None:
    async with engine.begin() as conn:
        key = await create_api_key(conn, "shop")
    resp = await client.get(
        "/internal/v1/config/snapshot", headers={"Authorization": "Bearer test-internal-token"}
    )
    assert resp.json()["api_keys"] == [{"id": key.id, "name": "shop", "sha256": key.sha256}]


# Tenant tokens


def test_tenant_token_expiry_is_issue_time_plus_ttl() -> None:
    now = datetime(2026, 10, 9, 12, 0, tzinfo=UTC)
    issued = TenantTokens(SECRET, 3600, now=lambda: now).issue("merchant-a")
    assert issued.expires_at == now + timedelta(hours=1)


def test_tenant_token_round_trip() -> None:
    tokens = TenantTokens(SECRET, 3600)
    assert tokens.verify(tokens.issue("merchant-a").token) == "merchant-a"


def test_expired_token_rejected() -> None:
    issued_long_ago = TenantTokens(
        SECRET, 3600, now=lambda: datetime.now(UTC) - timedelta(hours=2)
    ).issue("merchant-a")
    with pytest.raises(ProblemError) as exc:
        TenantTokens(SECRET, 3600).verify(issued_long_ago.token)
    assert exc.value.status == 401


def _token(claims: dict[str, object], secret: str = SECRET) -> str:
    return jwt.encode(claims, secret, algorithm="HS256")


def _claims(**overrides: object) -> dict[str, object]:
    now = int(datetime.now(UTC).timestamp())
    base: dict[str, object] = {
        "sub": "merchant-a",
        "aud": TenantTokens.AUDIENCE,
        "iss": TenantTokens.ISSUER,
        "iat": now,
        "exp": now + 3600,
    }
    return base | overrides


@pytest.mark.parametrize(
    "token",
    [
        _token(_claims(), secret="another-secret-another-secret-0123"),
        _token(_claims(aud="other")),
        _token(_claims(iss="other")),
        _token({k: v for k, v in _claims().items() if k != "exp"}),
        "not-a-jwt",
    ],
    ids=["foreign-secret", "wrong-aud", "wrong-iss", "no-exp", "garbage"],
)
def test_bad_tenant_tokens_rejected(token: str) -> None:
    with pytest.raises(ProblemError) as exc:
        TenantTokens(SECRET, 3600).verify(token)
    assert exc.value.status == 401


# Console sessions endpoint


async def operator_key(engine: AsyncEngine, name: str = "op") -> str:
    async with engine.begin() as conn:
        return (await create_api_key(conn, name)).token


def bearer(token: str) -> dict[str, str]:
    return {"Authorization": f"Bearer {token}"}


async def test_console_session_for_existing_tenant(
    client: httpx.AsyncClient, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        await add_tenant(conn, "merchant-a")
    token = await operator_key(engine)
    resp = await client.post("/api/v1/tenants/merchant-a/console-sessions", headers=bearer(token))
    assert resp.status_code == 201
    body = resp.json()
    assert TenantTokens(SECRET, 3600).verify(body["token"]) == "merchant-a"
    assert datetime.fromisoformat(body["expires_at"]) > datetime.now(UTC)


async def test_console_session_unknown_tenant(
    client: httpx.AsyncClient, engine: AsyncEngine
) -> None:
    token = await operator_key(engine)
    resp = await client.post("/api/v1/tenants/nobody/console-sessions", headers=bearer(token))
    assert resp.status_code == 404
    assert resp.headers["content-type"].startswith("application/problem+json")


async def test_console_session_needs_operator_key(
    client: httpx.AsyncClient, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        await add_tenant(conn, "merchant-a")
    tenant_token = TenantTokens(SECRET, 3600).issue("merchant-a").token
    url = "/api/v1/tenants/merchant-a/console-sessions"
    assert (await client.post(url)).status_code == 401
    assert (await client.post(url, headers=bearer("whk_unknown"))).status_code == 401
    assert (await client.post(url, headers=bearer(tenant_token))).status_code == 401


async def test_revoked_key_rejected(client: httpx.AsyncClient, engine: AsyncEngine) -> None:
    async with engine.begin() as conn:
        await add_tenant(conn, "merchant-a")
        key = await create_api_key(conn, "op")
        await revoke_key(conn, key.id)
    resp = await client.post(
        "/api/v1/tenants/merchant-a/console-sessions", headers=bearer(key.token)
    )
    assert resp.status_code == 401


async def test_tenant_auth_rejects_operator_key(engine: AsyncEngine) -> None:
    app = FastAPI()
    install_error_handlers(app)
    tenant_auth = TenantAuth(TenantTokens(SECRET, 3600))

    @app.get("/whoami")
    async def whoami(who: Annotated[TenantPrincipal, Depends(tenant_auth)]) -> str:
        return who.external_id

    key = await operator_key(engine)
    async with (
        LifespanManager(app),
        httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url="http://t") as c,
    ):
        assert (await c.get("/whoami", headers=bearer(key))).status_code == 401
        good = TenantTokens(SECRET, 3600).issue("merchant-a").token
        resp = await c.get("/whoami", headers=bearer(good))
        assert resp.status_code == 200
        assert resp.json() == "merchant-a"


# CLI


def test_cli_apikey_create(
    pg_url: str, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setenv("CP_DATABASE_URL", pg_url)
    monkeypatch.setenv("CP_INTERNAL_TOKEN", "test-internal-token")
    monkeypatch.setenv("CP_TENANT_JWT_SECRET", SECRET)
    main(["apikey", "create", "--name", "cli-key"])
    out = capsys.readouterr()
    lines = out.out.strip().splitlines()
    assert len(lines) == 1
    assert re.fullmatch(r"whk_[0-9A-Za-z]{32}", lines[0])
    assert "cli-key" in out.err
