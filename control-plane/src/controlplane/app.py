from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from sqlalchemy import text

from controlplane.db import create_engine
from controlplane.errors import install_error_handlers, problem_response
from controlplane.settings import Settings


def create_app(settings: Settings) -> FastAPI:
    engine = create_engine(settings.database_url)

    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        try:
            yield
        finally:
            await engine.dispose()

    app = FastAPI(title="WasmHooks control plane", lifespan=lifespan)
    app.state.settings = settings
    app.state.engine = engine
    install_error_handlers(app)

    @app.get("/healthz", include_in_schema=False)
    async def healthz() -> dict[str, str]:
        return {"status": "ok"}

    @app.get("/readyz", include_in_schema=False)
    async def readyz(request: Request) -> JSONResponse:
        try:
            async with request.app.state.engine.connect() as conn:
                await conn.execute(text("SELECT 1"))
        except Exception:
            return problem_response(503, "База данных недоступна")
        return JSONResponse({"status": "ready"})

    return app


def create_app_from_env() -> FastAPI:
    return create_app(Settings())
