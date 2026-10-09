import json

import httpx
import pytest

from controlplane.configstate.schemas import HookDefOut
from controlplane.dpclient.client import DataPlaneClient, DataPlaneError, DataPlaneUnavailable
from controlplane.dpclient.schemas import ValidationReport
from tests.factories import INPUT_SCHEMA, OUTPUT_SCHEMA, SAMPLE_INPUT

HASH = "sha256:" + "ab" * 32
HOOK = HookDefOut(
    name="checkout.discount",
    def_version=2,
    input_schema=INPUT_SCHEMA,
    output_schema=OUTPUT_SCHEMA,
    timeout_ms=50,
    memory_max_pages=64,
    allowed_host_functions=[],
    allowed_effect_types=[],
    sample_input=SAMPLE_INPUT,
)
REPORT = {"ok": True, "checks": [{"name": "fetch", "ok": True}]}


def client_with(transport: httpx.MockTransport) -> DataPlaneClient:
    return DataPlaneClient("http://dp", "secret-token", transport=transport)


async def test_request_shape_without_config() -> None:
    seen: list[httpx.Request] = []

    def handle(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(200, json=REPORT)

    report = await client_with(httpx.MockTransport(handle)).validate(HASH, HOOK)
    request = seen[0]
    assert request.method == "POST"
    assert str(request.url) == "http://dp/internal/v1/modules/validate"
    assert request.headers["Authorization"] == "Bearer secret-token"
    assert json.loads(request.content) == {
        "module_hash": HASH,
        "hook": HOOK.model_dump(mode="json"),
    }
    assert report == ValidationReport.model_validate(REPORT)


async def test_config_sent_only_when_given() -> None:
    bodies: list[dict[str, object]] = []

    def handle(request: httpx.Request) -> httpx.Response:
        bodies.append(json.loads(request.content))
        return httpx.Response(200, json=REPORT)

    client = client_with(httpx.MockTransport(handle))
    await client.validate(HASH, HOOK, {"percent": "10"})
    await client.validate(HASH, HOOK, {})
    assert bodies[0]["config"] == {"percent": "10"}
    assert bodies[1]["config"] == {}


async def test_503_is_unavailable() -> None:
    client = client_with(httpx.MockTransport(lambda r: httpx.Response(503)))
    with pytest.raises(DataPlaneUnavailable):
        await client.validate(HASH, HOOK)


async def test_connect_error_is_unavailable() -> None:
    def handle(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("refused", request=request)

    with pytest.raises(DataPlaneUnavailable):
        await client_with(httpx.MockTransport(handle)).validate(HASH, HOOK)


async def test_timeout_is_unavailable() -> None:
    def handle(request: httpx.Request) -> httpx.Response:
        raise httpx.ReadTimeout("slow", request=request)

    with pytest.raises(DataPlaneUnavailable):
        await client_with(httpx.MockTransport(handle)).validate(HASH, HOOK)


async def test_400_is_error_with_body_text() -> None:
    client = client_with(httpx.MockTransport(lambda r: httpx.Response(400, text="bad hook")))
    with pytest.raises(DataPlaneError, match="bad hook"):
        await client.validate(HASH, HOOK)
