# control-plane

Control plane WasmHooks: API-ключи, тенанты, хуки, модули, привязки, квоты. Решает, что разрешено; в пути вызова не участвует.

- Владелец: backend, Python (см. `docs/TEAM.md`).
- Реализует: `api/dataplane-internal.openapi.yaml` (снимок конфигурации для data plane).
- Потребляет: `POST /internal/v1/modules/validate` из того же контракта.
- Публикует: `api/control-plane.openapi.yaml` (API консолей, генерируется из FastAPI).
- Реализация ведётся backend, Python, начиная с M0.

## Как запустить

Нужны Python 3.13, [uv](https://docs.astral.sh/uv/) и Docker (тесты поднимают PostgreSQL через testcontainers).

```bash
cd control-plane
uv sync                 # зависимости
uv run pytest           # тесты (нужен запущенный Docker)
uv run ruff format . && uv run ruff check . && uv run mypy
```

Весь стенд (PostgreSQL, MinIO, control plane) из корня репозитория:

```bash
docker compose up -d --build --wait control-plane
```

Локальная разработка против PostgreSQL из compose:

```bash
docker compose up -d --wait postgres
CP_DATABASE_URL=postgresql+asyncpg://wasmhooks:wasmhooks@localhost:5432/controlplane \
CP_INTERNAL_TOKEN=dev CP_TENANT_JWT_SECRET=dev-tenant-jwt-secret-change-me-0123456789 \
  uv run uvicorn --factory controlplane.app:create_app_from_env --reload
```
