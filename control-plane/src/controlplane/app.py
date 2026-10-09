from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from sqlalchemy import text

from controlplane.apikeys import router as apikeys_router
from controlplane.auth import router as auth_router
from controlplane.auth.deps import InternalAuth, OperatorAuth, TenantAuth
from controlplane.auth.tenant_tokens import TenantTokens
from controlplane.bindings import router as bindings_router
from controlplane.configstate import router as configstate_router
from controlplane.configstate.snapshot import SnapshotService
from controlplane.configstate.watcher import VersionWatcher
from controlplane.db import create_engine
from controlplane.errors import (
    PROBLEM_RESPONSES,
    install_error_handlers,
    install_problem_schema,
    problem_response,
)
from controlplane.hooks import router as hooks_router
from controlplane.hooks.service import HookService
from controlplane.modules import router as modules_router
from controlplane.settings import Settings
from controlplane.tenantconsole import router as tenantconsole_router
from controlplane.tenants import router as tenants_router


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
    tenant_auth = TenantAuth(tenant_tokens)

    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        await watcher.start()
        try:
            yield
        finally:
            await watcher.stop()
            await engine.dispose()

    app = FastAPI(
        title="WasmHooks Control Plane API",
        version="0.1.0",
        description=(
            "API консолей оператора и тенанта. Владелец контракта: backend, Python "
            "(docs/TEAM.md); файл api/control-plane.openapi.yaml генерируется из кода "
            "командой `controlplane openapi`."
        ),
        lifespan=lifespan,
        responses=PROBLEM_RESPONSES,
    )
    app.state.settings = settings
    app.state.engine = engine
    install_error_handlers(app)
    install_problem_schema(app)

    app.include_router(configstate_router.build_router(snapshots, watcher, internal_auth))
    app.include_router(auth_router.build_router(engine, tenant_tokens, operator_auth))
    app.include_router(hooks_router.build_router(HookService(engine), operator_auth))
    # Stubs answering 501 until the backend tasks T1-T6 implement them.
    app.include_router(tenants_router.build_router(operator_auth))
    app.include_router(apikeys_router.build_router(operator_auth))
    app.include_router(tenantconsole_router.build_router(tenant_auth))
    app.include_router(modules_router.build_router(tenant_auth))
    app.include_router(bindings_router.build_router(tenant_auth))

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
