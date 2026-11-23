import json

import httpx
import pytest

from tests.conftest import API_KEY, FakePlatform, Handler

URL = "/api/v1/shops/merchant-a/console-sessions"
SESSION = {"token": "eyJ.tenant.jwt", "expires_at": "2026-11-22T13:00:00Z"}


def control_plane(tenant_status: int = 201, session: httpx.Response | None = None) -> Handler:
    """A control plane that answers the tenant registration and the session."""

    def handle(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/api/v1/tenants":
            return httpx.Response(tenant_status, json={"title": "x"})
        return session or httpx.Response(201, json=SESSION)

    return handle


@pytest.mark.parametrize("tenant_status", [201, 409], ids=["new-tenant", "existing-tenant"])
async def test_registers_the_merchant_and_issues_its_console_token(
    client: httpx.AsyncClient, platform: FakePlatform, tenant_status: int
) -> None:
    platform.control_plane = control_plane(tenant_status)
    resp = await client.post(URL)
    assert resp.status_code == 201, resp.text
    assert resp.json() == SESSION

    register, session = platform.control_plane_requests
    assert (register.method, str(register.url)) == ("POST", "http://cp/api/v1/tenants")
    assert json.loads(register.content) == {"external_id": "merchant-a", "name": "Чайная лавка"}
    assert (session.method, str(session.url)) == (
        "POST",
        "http://cp/api/v1/tenants/merchant-a/console-sessions",
    )
    for request in (register, session):
        assert request.headers["Authorization"] == f"Bearer {API_KEY}"


async def test_each_shop_gets_its_own_merchants_console(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    platform.control_plane = control_plane()
    resp = await client.post("/api/v1/shops/merchant-b/console-sessions")
    assert resp.status_code == 201, resp.text
    register, session = platform.control_plane_requests
    assert json.loads(register.content)["external_id"] == "merchant-b"
    assert session.url.path == "/api/v1/tenants/merchant-b/console-sessions"


async def test_unknown_shop_is_404_without_calling_the_control_plane(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    resp = await client.post("/api/v1/shops/merchant-x/console-sessions")
    assert resp.status_code == 404
    assert resp.json()["title"] == "Магазин не найден"
    assert platform.control_plane_requests == []


def refuse(request: httpx.Request) -> httpx.Response:
    raise httpx.ConnectError("connect to control-plane:8000 refused", request=request)


@pytest.mark.parametrize(
    "answer",
    [
        control_plane(tenant_status=401),
        control_plane(tenant_status=500),
        control_plane(session=httpx.Response(404, json={"title": "Тенант не найден"})),
        control_plane(session=httpx.Response(500, text="Traceback at control-plane:8000")),
        control_plane(session=httpx.Response(201, json={"token": "t"})),
        control_plane(
            session=httpx.Response(201, content=b"x", headers={"Content-Encoding": "gzip"})
        ),
        refuse,
    ],
    ids=[
        "register-401",
        "register-500",
        "session-404",
        "session-500",
        "malformed-session",
        "undecodable",
        "unreachable",
    ],
)
async def test_control_plane_failure_is_503_without_internal_details(
    client: httpx.AsyncClient, platform: FakePlatform, answer: Handler
) -> None:
    platform.control_plane = answer
    resp = await client.post(URL)
    assert resp.status_code == 503
    assert resp.headers["content-type"] == "application/problem+json"
    assert resp.json()["title"] == "Консоль скриптов недоступна"
    assert "detail" not in resp.json()
