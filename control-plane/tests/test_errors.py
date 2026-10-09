from collections.abc import AsyncIterator

import httpx
import pytest_asyncio
from fastapi import FastAPI
from pydantic import BaseModel
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.errors import ProblemError, install_error_handlers

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

    return app


@pytest_asyncio.fixture
async def err_client() -> AsyncIterator[httpx.AsyncClient]:
    transport = httpx.ASGITransport(app=_build_app())
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


async def test_fixture_isolation_a_writes(client: httpx.AsyncClient, engine: AsyncEngine) -> None:
    async with engine.begin() as conn:
        await conn.execute(text("CREATE TABLE IF NOT EXISTS _fixture_probe (id int)"))
        await conn.execute(text("INSERT INTO _fixture_probe VALUES (1)"))


async def test_fixture_isolation_b_sees_clean_db(
    client: httpx.AsyncClient, engine: AsyncEngine
) -> None:
    async with engine.begin() as conn:
        await conn.execute(text("CREATE TABLE IF NOT EXISTS _fixture_probe (id int)"))
        count = await conn.scalar(text("SELECT count(*) FROM _fixture_probe"))
    assert count == 0
