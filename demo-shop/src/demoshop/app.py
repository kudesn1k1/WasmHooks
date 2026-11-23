from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

import httpx
from fastapi import FastAPI

from demoshop.catalog import router as catalog_router
from demoshop.catalog.data import CUSTOMERS, PRODUCTS, SHOPS
from demoshop.catalog.service import Catalog
from demoshop.checkout import router as checkout_router
from demoshop.checkout.service import CheckoutService
from demoshop.console import router as console_router
from demoshop.console.client import ControlPlaneClient
from demoshop.errors import install_error_handlers, install_problem_schema
from demoshop.invoke.client import InvokeClient
from demoshop.settings import Settings


def create_app(
    settings: Settings,
    *,
    dataplane_transport: httpx.AsyncBaseTransport | None = None,
    control_plane_transport: httpx.AsyncBaseTransport | None = None,
) -> FastAPI:
    """Builds the application. This is the only place where components are
    wired together. The transports let tests stand in for the platform."""
    api_key = settings.api_key.get_secret_value()
    invoke = InvokeClient(settings.dataplane_url, api_key, transport=dataplane_transport)
    control_plane = ControlPlaneClient(
        settings.control_plane_url,
        api_key,
        settings.control_plane_timeout_s,
        transport=control_plane_transport,
    )
    catalog = Catalog(SHOPS, PRODUCTS, CUSTOMERS)
    checkout = CheckoutService(
        catalog, invoke, hook=settings.discount_hook, deadline_ms=settings.discount_deadline_ms
    )

    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        try:
            yield
        finally:
            await invoke.aclose()
            await control_plane.aclose()

    app = FastAPI(
        title="WasmHooks Demo Shop API",
        version="0.1.0",
        description=(
            "API демо-маркетплейса для витрины. Маркетплейс — оператор платформы, его "
            "магазины — её тенанты: чекаут магазина вызывает хук checkout.discount со "
            "скриптом своего мерчанта. Суммы — в копейках. Владелец контракта: backend, Python "
            "(docs/TEAM.md); файл api/demo-shop.openapi.yaml генерируется из кода командой "
            "`demoshop openapi`."
        ),
        lifespan=lifespan,
    )
    install_error_handlers(app)
    install_problem_schema(app)

    app.include_router(catalog_router.build_router(catalog))
    app.include_router(checkout_router.build_router(checkout))
    app.include_router(console_router.build_router(control_plane, catalog))

    @app.get("/healthz", include_in_schema=False)
    async def healthz() -> dict[str, str]:
        return {"status": "ok"}

    return app


def create_app_from_env() -> FastAPI:
    return create_app(Settings())
