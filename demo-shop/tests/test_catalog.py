import httpx

from demoshop.catalog.data import CUSTOMERS, PRODUCTS, SHOPS


async def test_shops_list_the_merchants(client: httpx.AsyncClient) -> None:
    resp = await client.get("/api/v1/shops")
    assert resp.status_code == 200, resp.text
    items = resp.json()["items"]
    assert [s["id"] for s in items] == ["merchant-a", "merchant-b"]
    assert set(items[0]) == {"id", "name", "description"}


async def test_each_shop_lists_only_its_own_products(client: httpx.AsyncClient) -> None:
    tea = (await client.get("/api/v1/shops/merchant-a/products")).json()["items"]
    cafe = (await client.get("/api/v1/shops/merchant-b/products")).json()["items"]
    assert [p["id"] for p in tea] == ["tea-sencha", "teapot-glass", "honey-linden"]
    assert [p["id"] for p in cafe] == ["coffee-ethiopia", "cezve-copper", "mug-ceramic"]
    assert {p["shop_id"] for p in tea} == {"merchant-a"}
    assert set(tea[0]) == {"id", "shop_id", "name", "description", "price"}
    assert all(isinstance(p["price"], int) and p["price"] > 0 for p in tea + cafe)


async def test_unknown_shop_has_no_catalog(client: httpx.AsyncClient) -> None:
    resp = await client.get("/api/v1/shops/merchant-x/products")
    assert resp.status_code == 404
    assert resp.headers["content-type"] == "application/problem+json"
    assert resp.json()["title"] == "Магазин не найден"
    assert resp.json()["detail"] == "merchant-x"


async def test_customers_list_demo_buyers(client: httpx.AsyncClient) -> None:
    resp = await client.get("/api/v1/customers")
    assert resp.status_code == 200, resp.text
    items = resp.json()["items"]
    assert [c["id"] for c in items] == [c.id for c in CUSTOMERS]
    assert set(items[0]) == {"id", "name", "lifetime_spend"}


def test_demo_buyers_straddle_the_template_threshold() -> None:
    # examples/scripts/rust/discount: 10 % from 1000 rubles of past purchases.
    spends = [c.lifetime_spend for c in CUSTOMERS]
    assert min(spends) < 1000_00 <= max(spends)


def test_catalog_is_consistent() -> None:
    shop_ids = {s.id for s in SHOPS}
    assert len(shop_ids) == len(SHOPS)
    assert len({p.id for p in PRODUCTS}) == len(PRODUCTS)
    assert len({c.id for c in CUSTOMERS}) == len(CUSTOMERS)
    assert {p.shop_id for p in PRODUCTS} == shop_ids
