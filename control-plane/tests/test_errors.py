from collections.abc import AsyncIterator

import httpx
import pytest_asyncio
from fastapi import FastAPI
from pydantic import BaseModel
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.errors import ProblemError, install_error_handlers
from tests.conftest import reset_db

PROBLEM = "application/problem+json"


class Body(BaseModel):
    n: int


def _build_app() -> FastAPI:
    app = FastAPI()
    install_error_handlers(app)

    @app.get("/conflict")
    async def conflict() -> None:
        raise ProblemError(409, "Конфликт", "x")

    @app.get("/query")
    async def query(n: int) -> dict[str, int]:
        return {"n": n}

    @app.post("/body")
    async def body(b: Body) -> dict[str, int]:
        return {"n": b.n}

    @app.get("/todo")
    async def todo() -> None:
        raise NotImplementedError("T1")

    @app.get("/boom")
    async def boom() -> None:
        raise RuntimeError("unexpected")

    @app.get("/challenge")
    async def challenge() -> None:
        raise ProblemError(401, "Не авторизован", headers={"WWW-Authenticate": "Bearer"})

    return app


@pytest_asyncio.fixture
async def err_client() -> AsyncIterator[httpx.AsyncClient]:
    # The 500 handler responds, then Starlette re-raises for logging.
    transport = httpx.ASGITransport(app=_build_app(), raise_app_exceptions=False)
    async with httpx.AsyncClient(transport=transport, base_url="http://t") as c:
        yield c


async def test_problem_error(err_client: httpx.AsyncClient) -> None:
    r = await err_client.get("/conflict")
    assert r.status_code == 409
    assert r.headers["content-type"].startswith(PROBLEM)
    assert r.json() == {"type": "about:blank", "title": "Конфликт", "status": 409, "detail": "x"}


async def test_bad_query_is_400(err_client: httpx.AsyncClient) -> None:
    r = await err_client.get("/query", params={"n": "abc"})
    assert r.status_code == 400
    assert r.headers["content-type"].startswith(PROBLEM)
    assert r.json()["detail"].startswith("query.n: ")


async def test_bad_body_is_422(err_client: httpx.AsyncClient) -> None:
    r = await err_client.post("/body", json={"n": "abc"})
    assert r.status_code == 422
    assert r.headers["content-type"].startswith(PROBLEM)


async def test_not_implemented_is_501(err_client: httpx.AsyncClient) -> None:
    r = await err_client.get("/todo")
    assert r.status_code == 501
    assert r.headers["content-type"].startswith(PROBLEM)
    assert r.json()["detail"] == "T1"


async def test_unknown_path_is_404_problem(err_client: httpx.AsyncClient) -> None:
    r = await err_client.get("/nope")
    assert r.status_code == 404
    assert r.headers["content-type"].startswith(PROBLEM)
    assert r.json()["title"] == "Not Found"


async def test_reset_db_leaves_a_clean_database(engine: AsyncEngine) -> None:
    # What every test relies on: client -> settings -> engine -> reset_db.
    async with engine.begin() as conn:
        await conn.execute(text("INSERT INTO tenants (external_id, name) VALUES ('t', 't')"))
        await conn.execute(text("UPDATE config_state SET version = 5"))
    await reset_db(engine)
    async with engine.connect() as conn:
        assert await conn.scalar(text("SELECT count(*) FROM tenants")) == 0
        assert await conn.scalar(text("SELECT version FROM config_state")) == 1


async def test_unexpected_error_is_500_problem(err_client: httpx.AsyncClient) -> None:
    r = await err_client.get("/boom")
    assert r.status_code == 500
    assert r.headers["content-type"].startswith(PROBLEM)


async def test_problem_error_headers_are_sent(err_client: httpx.AsyncClient) -> None:
    r = await err_client.get("/challenge")
    assert r.status_code == 401
    assert r.headers["www-authenticate"] == "Bearer"
