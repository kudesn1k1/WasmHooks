# demo-shop

Бэкенд демо-маркетплейса WasmHooks. Маркетплейс — оператор платформы, его магазины — её тенанты. Каждый магазин подключает свои скрипты на хук `checkout.discount` независимо от других. Чекаут магазина вызывает хук со скриптом своего мерчанта и применяет посчитанную скидку. Витрина — route group `(storefront)` в `web/`.

- Владелец: backend, Python (см. `docs/TEAM.md`).
- Публикует: `api/demo-shop.openapi.yaml`, генерируется из кода: `uv run demoshop openapi > ../api/demo-shop.openapi.yaml`. CI проверяет, что файл совпадает с кодом.
- Потребляет: `api/invoke.openapi.yaml` (Invoke data plane) и API оператора из `api/control-plane.openapi.yaml` (`POST /api/v1/tenants`, `console-sessions`).

## API для витрины

| Эндпоинт | Что делает |
|---|---|
| `GET /api/v1/shops` | Магазины. `id` магазина — `external_id` его мерчанта в платформе. |
| `GET /api/v1/shops/{shop_id}/products` | Каталог магазина. Цены в копейках. |
| `GET /api/v1/customers` | Демо-покупатели с суммой прошлых покупок. Учётных записей нет, витрина даёт выбрать, кто покупает. |
| `POST /api/v1/shops/{shop_id}/checkout` | Корзина `{customer_id, items: [{product_id, quantity}]}` → строки, `subtotal`, `discount`, `total`. Товары только этого магазина. Заказ не сохраняется. |
| `POST /api/v1/shops/{shop_id}/console-sessions` | Кнопка «Мои скрипты»: `{token, expires_at}` консоли тенанта, витрина открывает `/tenant#token=<token>`. |

Скидка 0 при любом исходе хука, кроме `ok`, — нормальный путь: `discount.fallback = true`, в `discount.outcome` исход Invoke или `error` (сеть, истёк дедлайн, data plane отклонил запрос). Чекаут от скрипта не ломается и не ждёт его дольше `DS_DISCOUNT_DEADLINE_MS`.

«Мои скрипты» сначала регистрирует мерчанта тенантом (`POST /api/v1/tenants`, уже существующий — не ошибка), затем выписывает токен. Создавать тенантов руками не нужно. Пока мерчант не зарегистрирован, его чекаут получает `no_handler` и скидку 0.

Маркетплейс без состояния: магазины, каталоги и покупатели — неизменяемые данные в `catalog/data.py`, корзину держит витрина.

## Настройки

Только переменные окружения с префиксом `DS_`:

| Переменная | По умолчанию | Что это |
|---|---|---|
| `DS_API_KEY` | — (обязательна) | Ключ установки маркетплейса (`whk_...`) |
| `DS_DATAPLANE_URL` | `http://localhost:8080` | Публичный Invoke data plane |
| `DS_CONTROL_PLANE_URL` | `http://localhost:8000` | Control plane |
| `DS_DISCOUNT_HOOK` | `checkout.discount` | Хук скидки |
| `DS_DISCOUNT_DEADLINE_MS` | `200` | Бюджет чекаута на хук, сеть включительно |

## Запуск

Тесты и линтеры (data plane и control plane подменяются `httpx.MockTransport`, Docker не нужен):

```bash
cd demo-shop
uv sync
uv run pytest
uv run ruff format . && uv run ruff check . && uv run mypy
```

Вместе с платформой в compose, из корня репозитория. Магазину нужен ключ установки, поэтому он в профиле `shop` и поднимается после платформы:

```bash
docker compose up -d --build --wait control-plane dataplane
export SHOP_API_KEY=$(docker compose exec -T control-plane controlplane apikey create --name shop)
# хук checkout.discount: docs/handoff/backend.md, раздел 3
docker compose --profile shop up -d --build --wait demo-shop
curl -s -X POST localhost:8001/api/v1/shops/merchant-a/checkout -H "Content-Type: application/json" \
  -d '{"customer_id":"c-loyal","items":[{"product_id":"tea-sencha","quantity":2}]}'
```

Пока у мерчанта нет активного скрипта, скидка 0 с `outcome: no_handler`. Дальше через «Мои скрипты» каждого магазина загрузите и активируйте `dataplane/testdata/wasm/discount.wasm`. Скрипт берёт порог и процент из конфига привязки (`threshold`, `percent`), поэтому у разных магазинов один модуль может давать разные скидки. Если на хосте задан `HTTP_PROXY`, вызывайте curl с `--noproxy '*'`. Сам магазин прокси из окружения не использует.

Локально с `--reload`:

```bash
DS_API_KEY=whk_... uv run uvicorn --factory demoshop.app:create_app_from_env --port 8001 --reload
```
