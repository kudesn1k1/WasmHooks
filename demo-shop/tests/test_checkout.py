import asyncio
import json
import time
from typing import Any

import httpx
import pytest
from asgi_lifespan import LifespanManager

from demoshop.app import create_app
from demoshop.settings import Settings
from tests.conftest import API_KEY, FakePlatform, discount, invoke_response

URL = "/api/v1/shops/merchant-a/checkout"
# Чайная лавка: 2 x 450 + 1 x 650 rubles = 1550 rubles.
CART = {
    "customer_id": "c-loyal",
    "items": [
        {"product_id": "tea-sencha", "quantity": 2},
        {"product_id": "honey-linden", "quantity": 1},
    ],
}
SUBTOTAL = 155000


def assert_no_discount(body: dict[str, Any], outcome: str) -> None:
    assert body["subtotal"] == SUBTOTAL
    assert body["total"] == SUBTOTAL
    assert body["discount"] == {
        "percent": 0,
        "amount": 0,
        "reason": None,
        "outcome": outcome,
        "fallback": True,
    }


async def test_ok_applies_the_scripts_discount(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    platform.dataplane = lambda _: discount(10)
    resp = await client.post(URL, json=CART)
    assert resp.status_code == 200, resp.text
    assert resp.json() == {
        "shop_id": "merchant-a",
        "customer_id": "c-loyal",
        "lines": [
            {
                "product_id": "tea-sencha",
                "name": "Чай сенча",
                "unit_price": 45000,
                "quantity": 2,
                "line_total": 90000,
            },
            {
                "product_id": "honey-linden",
                "name": "Липовый мёд",
                "unit_price": 65000,
                "quantity": 1,
                "line_total": 65000,
            },
        ],
        "subtotal": SUBTOTAL,
        "discount": {
            "percent": 10,
            "amount": 15500,
            "reason": "loyal_customer",
            "outcome": "ok",
            "fallback": False,
        },
        "total": SUBTOTAL - 15500,
    }


async def test_invoke_request_follows_the_hook_input_schema(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    platform.dataplane = lambda _: discount(10)
    assert (await client.post(URL, json=CART)).status_code == 200
    [request] = platform.dataplane_requests
    assert request.method == "POST"
    assert str(request.url) == "http://dp/v1/hooks/checkout.discount/invoke"
    assert request.headers["Authorization"] == f"Bearer {API_KEY}"
    assert json.loads(request.content) == {
        "tenant_id": "merchant-a",
        "payload": {"cart_total": 1550.0, "customer": {"id": "c-loyal", "lifetime_spend": 2400.0}},
        # 20 ms below the shop's own cutoff, so that the answer still arrives.
        "deadline_ms": 180,
    }


@pytest.mark.parametrize(
    ("outcome", "status"),
    [
        ("no_handler", 200),
        ("timeout", 200),
        ("handler_error", 200),
        ("quota_exceeded", 429),
        ("unavailable", 503),
    ],
)
async def test_any_other_outcome_means_no_discount(
    client: httpx.AsyncClient, platform: FakePlatform, outcome: str, status: int
) -> None:
    platform.dataplane = lambda _: invoke_response(outcome, status, error="boom")
    resp = await client.post(URL, json=CART)
    assert resp.status_code == 200, resp.text
    assert_no_discount(resp.json(), outcome)


@pytest.mark.parametrize("status", [400, 401, 404, 413, 500, 502])
async def test_data_plane_rejecting_the_request_means_no_discount(
    client: httpx.AsyncClient, platform: FakePlatform, status: int
) -> None:
    platform.dataplane = lambda _: httpx.Response(
        status, json={"type": "about:blank", "title": "nope", "status": status, "detail": "x"}
    )
    resp = await client.post(URL, json=CART)
    assert resp.status_code == 200, resp.text
    assert_no_discount(resp.json(), "error")


async def test_unreachable_data_plane_means_no_discount(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    def refuse(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused", request=request)

    platform.dataplane = refuse
    resp = await client.post(URL, json=CART)
    assert resp.status_code == 200, resp.text
    assert_no_discount(resp.json(), "error")


async def test_undecodable_invoke_body_means_no_discount(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    # A proxy in front of the data plane mangled the encoding.
    platform.dataplane = lambda _: httpx.Response(
        200, content=b"not gzip", headers={"Content-Encoding": "gzip"}
    )
    resp = await client.post(URL, json=CART)
    assert resp.status_code == 200, resp.text
    assert_no_discount(resp.json(), "error")


async def test_slow_data_plane_is_cut_at_the_deadline(
    settings: Settings, platform: FakePlatform
) -> None:
    async def hang(_: httpx.Request) -> httpx.Response:
        await asyncio.sleep(5)
        return discount(10)

    platform.dataplane = hang
    app = create_app(
        settings.model_copy(update={"discount_deadline_ms": 50}),
        dataplane_transport=platform.dataplane_transport(),
    )
    async with (
        LifespanManager(app),
        httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url="http://shop") as c,
    ):
        started = time.monotonic()
        resp = await c.post(URL, json=CART)
        elapsed = time.monotonic() - started
    assert resp.status_code == 200, resp.text
    assert_no_discount(resp.json(), "error")
    assert elapsed < 1


@pytest.mark.parametrize(
    ("status", "body"),
    [
        (200, b"not json"),
        (200, b'{"outcome": "teleported"}'),
        (200, b'{"result": null}'),
        (429, b"<html>Too Many Requests</html>"),
        (503, b"<html>Service Unavailable</html>"),
    ],
    ids=["not-json", "unknown-outcome", "no-outcome", "429-html", "503-html"],
)
async def test_malformed_invoke_answer_means_no_discount(
    client: httpx.AsyncClient, platform: FakePlatform, status: int, body: bytes
) -> None:
    platform.dataplane = lambda _: httpx.Response(status, content=body)
    resp = await client.post(URL, json=CART)
    assert resp.status_code == 200, resp.text
    assert_no_discount(resp.json(), "error")


@pytest.mark.parametrize(
    "result",
    [None, {"reason": "x"}, {"discount_percent": 150, "reason": "x"}, {"discount_percent": "10"}],
    ids=["null", "no-percent", "over-100", "string-percent-no-reason"],
)
async def test_ok_with_a_result_the_shop_cannot_use_means_no_discount(
    client: httpx.AsyncClient, platform: FakePlatform, result: dict[str, Any] | None
) -> None:
    platform.dataplane = lambda _: invoke_response("ok", result=result)
    resp = await client.post(URL, json=CART)
    assert resp.status_code == 200, resp.text
    assert_no_discount(resp.json(), "ok")


async def test_unknown_response_fields_are_ignored(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    platform.dataplane = lambda _: invoke_response(
        "ok",
        result={"discount_percent": 5, "reason": "promo", "extra": 1},
        trace_id="t-1",
    )
    resp = await client.post(URL, json=CART)
    assert resp.status_code == 200, resp.text
    assert resp.json()["discount"]["percent"] == 5
    assert resp.json()["total"] == SUBTOTAL - 7750


async def test_each_shop_calls_the_hook_as_its_own_tenant(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    # Each merchant's own script answers: the platform picks it by tenant_id.
    percents = {"merchant-a": 10, "merchant-b": 25}
    platform.dataplane = lambda r: discount(percents[json.loads(r.content)["tenant_id"]])
    coffee = {"customer_id": "c-loyal", "items": [{"product_id": "coffee-ethiopia", "quantity": 1}]}

    tea = (await client.post(URL, json=CART)).json()
    cafe = (await client.post("/api/v1/shops/merchant-b/checkout", json=coffee)).json()

    assert [json.loads(r.content)["tenant_id"] for r in platform.dataplane_requests] == [
        "merchant-a",
        "merchant-b",
    ]
    assert (tea["shop_id"], tea["discount"]["percent"]) == ("merchant-a", 10)
    assert (cafe["shop_id"], cafe["discount"]["percent"], cafe["total"]) == (
        "merchant-b",
        25,
        89000 - 22250,
    )


async def test_unknown_shop_is_404_without_calling_the_hook(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    resp = await client.post("/api/v1/shops/merchant-x/checkout", json=CART)
    assert resp.status_code == 404
    assert resp.json()["title"] == "Магазин не найден"
    assert resp.json()["detail"] == "merchant-x"
    assert platform.dataplane_requests == []


async def test_another_shops_product_is_404_without_calling_the_hook(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    items = [{"product_id": "coffee-ethiopia", "quantity": 1}]
    resp = await client.post(URL, json=CART | {"items": items})
    assert resp.status_code == 404
    assert resp.json()["title"] == "Товар не найден"
    assert resp.json()["detail"] == "coffee-ethiopia"
    assert platform.dataplane_requests == []


async def test_unknown_customer_is_404_without_calling_the_hook(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    resp = await client.post(URL, json=CART | {"customer_id": "c-ghost"})
    assert resp.status_code == 404
    assert resp.headers["content-type"] == "application/problem+json"
    assert resp.json()["title"] == "Покупатель не найден"
    assert resp.json()["detail"] == "c-ghost"
    assert platform.dataplane_requests == []


async def test_unknown_product_is_404_without_calling_the_hook(
    client: httpx.AsyncClient, platform: FakePlatform
) -> None:
    items = [{"product_id": "tea-sencha", "quantity": 1}, {"product_id": "x", "quantity": 1}]
    resp = await client.post(URL, json=CART | {"items": items})
    assert resp.status_code == 404
    assert resp.json()["title"] == "Товар не найден"
    assert resp.json()["detail"] == "x"
    assert platform.dataplane_requests == []


@pytest.mark.parametrize(
    "body",
    [
        {"customer_id": "c-new", "items": []},
        {"customer_id": "c-new", "items": [{"product_id": "tea-sencha", "quantity": 0}]},
        {"customer_id": "c-new", "items": [{"product_id": "tea-sencha", "quantity": 100}]},
        {"items": [{"product_id": "tea-sencha", "quantity": 1}]},
        CART | {"coupon": "FREE"},
    ],
    ids=["empty-cart", "zero-quantity", "too-many", "no-customer", "extra-field"],
)
async def test_malformed_cart_is_422(
    client: httpx.AsyncClient, platform: FakePlatform, body: dict[str, Any]
) -> None:
    resp = await client.post(URL, json=body)
    assert resp.status_code == 422, resp.text
    assert resp.headers["content-type"] == "application/problem+json"
    assert platform.dataplane_requests == []
