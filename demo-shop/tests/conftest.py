from collections.abc import AsyncIterator, Awaitable, Callable
from typing import Any

import httpx
import pytest
import pytest_asyncio
from asgi_lifespan import LifespanManager

from demoshop.app import create_app
from demoshop.settings import Settings

API_KEY = "whk_test-shop-key"

Handler = Callable[[httpx.Request], httpx.Response | Awaitable[httpx.Response]]


def invoke_response(outcome: str = "ok", status: int = 200, **fields: Any) -> httpx.Response:
    body: dict[str, Any] = {
        "outcome": outcome,
        "result": None,
        "effects": [],
        "error": None,
        "duration_ms": 3,
        "module_hash": None,
    } | fields
    return httpx.Response(status, json=body, headers={"X-Hook-Outcome": outcome})


def discount(percent: int, reason: str = "loyal_customer") -> httpx.Response:
    return invoke_response(
        result={"discount_percent": percent, "reason": reason},
        module_hash="sha256:" + "ab" * 32,
    )


class FakePlatform:
    """Stands in for the data plane and the control plane; records what the
    shop sent and answers with whatever the test set."""

    def __init__(self) -> None:
        self.dataplane: Handler = lambda _: invoke_response("no_handler")
        self.control_plane: Handler = lambda _: httpx.Response(404)
        self.dataplane_requests: list[httpx.Request] = []
        self.control_plane_requests: list[httpx.Request] = []

    async def _dataplane(self, request: httpx.Request) -> httpx.Response:
        self.dataplane_requests.append(request)
        return await _call(self.dataplane, request)

    async def _control_plane(self, request: httpx.Request) -> httpx.Response:
        self.control_plane_requests.append(request)
        return await _call(self.control_plane, request)

    def dataplane_transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self._dataplane)

    def control_plane_transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self._control_plane)


async def _call(handler: Handler, request: httpx.Request) -> httpx.Response:
    response = handler(request)
    return response if isinstance(response, httpx.Response) else await response


@pytest.fixture
def platform() -> FakePlatform:
    return FakePlatform()


@pytest.fixture
def settings() -> Settings:
    return Settings(
        api_key=API_KEY,
        dataplane_url="http://dp",
        control_plane_url="http://cp",
        discount_deadline_ms=200,
    )


@pytest_asyncio.fixture
async def client(settings: Settings, platform: FakePlatform) -> AsyncIterator[httpx.AsyncClient]:
    app = create_app(
        settings,
        dataplane_transport=platform.dataplane_transport(),
        control_plane_transport=platform.control_plane_transport(),
    )
    async with LifespanManager(app):
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url="http://shop") as c:
            yield c
