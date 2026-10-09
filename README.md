# WasmHooks

Self-hosted платформа, которая позволяет SaaS-продукту безопасно исполнять кастомную логику своих клиентов в заранее определённых точках расширения. Клиенты (тенанты) пишут обработчики на любом языке, компилирующемся в WebAssembly, платформа исполняет их в изолированной песочнице со строгими лимитами по памяти и времени и синхронно возвращает результат оператору. Готовый WebAssembly-рантайм берётся с рынка (Extism поверх wazero): предмет проекта — инфраструктура вокруг него: мультитенантность, квоты, горизонтальное масштабирование исполнителей, отказоустойчивость, версионирование модулей, наблюдаемость. Для демонстрации реализуется небольшой интернет-магазин с хуками расчёта скидки и валидации заказа.

## Структура репозитория

| Директория | Назначение |
|---|---|
| `api/` | контракты (OpenAPI), общие для команды |
| `dataplane/` | Go-модуль `github.com/kudesn1k1/WasmHooks/dataplane`: gateway и executor |
| `examples/scripts/rust/` | cargo workspace со скриптами-фикстурами и примерами |
| `control-plane/` | control plane (Python, FastAPI): фундамент M1, задачи backend — `docs/handoff/backend.md` |
| `sdk/python/` | Python SDK — задача T8 backend (пока README) |
| `demo-shop/` | бэкенд демо-магазина — задача T7 backend (пока README) |
| `web/` | фронтенд на Next.js: консоли оператора и тенанта, витрина |
| `docs/` | бриф, спеки, ADR, `TEAM.md` |
| `.github/workflows/ci.yml` | CI |

## Документы

- Бриф проекта: [`docs/project-brief.md`](docs/project-brief.md)
- Спеки (основной и по вехам): [`docs/superpowers/specs/`](docs/superpowers/specs/)
- Команда и контракты между компонентами: [`docs/TEAM.md`](docs/TEAM.md)

## Быстрый старт data plane

```bash
cd dataplane && go test ./...
go run ./cmd/demo
```

## Запуск (M1)

Вся платформа в Docker Compose из корня репозитория: PostgreSQL, MinIO, control plane и data plane. Нужен только Docker.

```bash
docker compose up -d --build --wait control-plane dataplane
```

Первый ключ установки (показывается один раз, сохраните его):

```bash
docker compose exec control-plane controlplane apikey create --name shop
```

Демо M1 доказывает утверждения вехи против поднятого compose: хук, созданный через API control plane, доходит до data plane без перезапуска; при остановленном control plane вызовы обслуживаются по последнему снимку; после возврата control plane data plane подхватывает следующее изменение; модуль из MinIO исполняется, Validate даёт верные вердикты. Таблица и код выхода 1 при любом FAIL; подробности в [`e2e/README.md`](e2e/README.md).

```bash
cd e2e && go run ./cmd/demo
```

Что где слушает:

| Адрес | Что |
|---|---|
| `http://localhost:8080` | data plane, публичный Invoke API (`api/invoke.openapi.yaml`): `POST /v1/hooks/{hook}/invoke`, `/healthz`, `/readyz` |
| `dataplane:8081` | внутренний API data plane (Validate), только внутри сети compose, наружу не публикуется |
| `http://localhost:8000` | control plane: API оператора и консолей (`api/control-plane.openapi.yaml`), Swagger UI на `/docs` |
| `localhost:5432` | PostgreSQL (`wasmhooks` / `wasmhooks`, база `controlplane`) |
| `http://localhost:9001` | консоль MinIO (`minioadmin` / `minioadmin`), бакет `modules` |

Значения по умолчанию переопределяются через `.env` (образец — `.env.example`). Остановить и удалить данные: `docker compose down -v`.

Дальше:

- control plane — запуск тестов, локальная разработка: [`control-plane/README.md`](control-plane/README.md);
- backend-разработчику — картина, эталонный срез, задачи T1–T8: [`docs/handoff/backend.md`](docs/handoff/backend.md).
