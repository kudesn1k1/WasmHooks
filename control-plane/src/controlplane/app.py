from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from sqlalchemy import text

from controlplane.auth import router as auth_router
from controlplane.auth.deps import InternalAuth, OperatorAuth
from controlplane.auth.tenant_tokens import TenantTokens
from controlplane.configstate import router as configstate_router
from controlplane.configstate.snapshot import SnapshotService
from controlplane.configstate.watcher import VersionWatcher
from controlplane.db import create_engine
from controlplane.errors import install_error_handlers, problem_response
from controlplane.settings import Settings


def create_app(settings: Settings) -> FastAPI:
    """Builds the application. This is the only place where components are
    wired together: every router gets its dependencies here, explicitly."""
    engine = create_engine(settings.database_url)
    watcher = VersionWatcher(engine, settings.snapshot_poll_interval_s)
    snapshots = SnapshotService(engine, watcher)
    internal_auth = InternalAuth(settings.internal_token.get_secret_value())
    operator_auth = OperatorAuth(engine)
    tenant_tokens = TenantTokens(
        settings.tenant_jwt_secret.get_secret_value(), settings.tenant_token_ttl_s
    )

    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        await watcher.start()
        try:
            yield
        finally:
            await watcher.stop()
            await engine.dispose()

    app = FastAPI(title="WasmHooks control plane", lifespan=lifespan)
    app.state.settings = settings
    app.state.engine = engine
    install_error_handlers(app)

    app.include_router(configstate_router.build_router(snapshots, watcher, internal_auth))
    app.include_router(auth_router.build_router(engine, tenant_tokens, operator_auth))

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
