import os
from collections.abc import AsyncIterator, Iterator

import boto3
import httpx
import pytest
import pytest_asyncio
from alembic import command
from asgi_lifespan import LifespanManager
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncEngine
from testcontainers.community.minio import MinioContainer
from testcontainers.community.postgres import PostgresContainer

from controlplane.app import create_app
from controlplane.cli import alembic_config
from controlplane.db import create_engine
from controlplane.settings import Settings

# Ryuk (the testcontainers reaper) is unreliable on Docker Desktop for Windows.
os.environ.setdefault("TESTCONTAINERS_RYUK_DISABLED", "true")


def run_migrations(url: str) -> None:
    command.upgrade(alembic_config(url), "head")


async def reset_db(engine: AsyncEngine) -> None:
    async with engine.begin() as conn:
        await conn.execute(text("DROP TABLE IF EXISTS _fixture_probe"))
        exists = await conn.scalar(text("SELECT to_regclass('public.config_state') IS NOT NULL"))
        if exists:
            await conn.execute(
                text("TRUNCATE bindings, modules, hooks, tenants, api_keys, config_changes CASCADE")
            )
            await conn.execute(text("UPDATE config_state SET version = 1"))


@pytest.fixture(scope="session")
def pg_url() -> Iterator[str]:
    with PostgresContainer("postgres:17", driver="asyncpg") as pg:
        url = pg.get_connection_url()
        run_migrations(url)
        yield url


@pytest_asyncio.fixture
async def engine(pg_url: str) -> AsyncIterator[AsyncEngine]:
    eng = create_engine(pg_url)
    await reset_db(eng)
    try:
        yield eng
    finally:
        await eng.dispose()


@pytest.fixture
def settings(pg_url: str, engine: AsyncEngine) -> Settings:
    return Settings(
        database_url=pg_url,
        internal_token="test-internal-token",
        tenant_jwt_secret="test-tenant-jwt-secret-0123456789abcdef",
        snapshot_poll_interval_s=0.05,
    )


@pytest_asyncio.fixture
async def client(settings: Settings) -> AsyncIterator[httpx.AsyncClient]:
    app = create_app(settings)
    async with LifespanManager(app):
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url="http://cp") as c:
            yield c


MINIO_BUCKET = "modules"


@pytest.fixture(scope="session")
def minio() -> Iterator[dict[str, str]]:
    # chainguard/minio: the official minio images can no longer be pulled. Its
    # non-root user cannot initialise the data dir, hence user="0".
    container = MinioContainer(image="chainguard/minio:latest").with_kwargs(user="0")
    container.with_env("MINIO_ROOT_USER", container.access_key)
    container.with_env("MINIO_ROOT_PASSWORD", container.secret_key)
    with container:
        config = container.get_config()
        endpoint = f"http://{config['endpoint']}"
        s3 = boto3.client(
            "s3",
            endpoint_url=endpoint,
            aws_access_key_id=config["access_key"],
            aws_secret_access_key=config["secret_key"],
            region_name="us-east-1",
        )
        s3.create_bucket(Bucket=MINIO_BUCKET)
        yield {
            "endpoint": endpoint,
            "bucket": MINIO_BUCKET,
            "access_key": config["access_key"],
            "secret_key": config["secret_key"],
            "region": "us-east-1",
        }
