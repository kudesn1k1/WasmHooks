import httpx
from asgi_lifespan import LifespanManager

from controlplane.app import create_app
from controlplane.settings import Settings


async def test_healthz(client: httpx.AsyncClient) -> None:
    r = await client.get("/healthz")
    assert r.status_code == 200
    assert r.json() == {"status": "ok"}


async def test_readyz_with_live_db(client: httpx.AsyncClient) -> None:
    r = await client.get("/readyz")
    assert r.status_code == 200


async def test_readyz_without_db_is_503_problem() -> None:
    settings = Settings(
        database_url="postgresql+asyncpg://u:p@127.0.0.1:1/x",
        internal_token="test-internal-token",
        tenant_jwt_secret="test-tenant-jwt-secret-0123456789abcdef",
    )
    app = create_app(settings)
    async with LifespanManager(app):
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url="http://cp") as c:
            r = await c.get("/readyz")
    assert r.status_code == 503
    assert r.headers["content-type"].startswith("application/problem+json")
    assert r.json()["status"] == 503
