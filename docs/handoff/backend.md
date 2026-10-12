# Backend: передача задач Milestone 1

Документ для backend-разработчика (Python). Здесь вся картина и упорядоченный список задач T1–T8 с критериями приёмки. По решению D26 спека M1 это единственный источник правды по задачам backend. Если GitHub Issues заведены, они ссылаются сюда.

Как читать: разделы 1–3 сразу, 4–5 перед первой задачей, 6 — по задаче, над которой работаете. Источники: спек M1 `docs/superpowers/specs/2026-10-09-milestone-1-design.md`, основной спек `docs/superpowers/specs/2026-09-04-wasm-extension-platform-design.md`, роли и контракты `docs/TEAM.md`.

Содержание:

1. [Картина](#1-картина)
2. [User stories](#2-user-stories)
3. [Как запустить](#3-как-запустить)
4. [Эталонный срез: хуки оператора](#4-эталонный-срез-хуки-оператора)
5. [Правила](#5-правила)
6. [Задачи T1–T8](#6-задачи-t1t8)
7. [Ограничения M1, которые вас касаются](#7-ограничения-m1-которые-вас-касаются)

---

## 1. Картина

### Что за платформа

WasmHooks — self-hosted платформа, которую SaaS-приложение (**оператор**, у нас это демо-магазин) разворачивает рядом с собой, чтобы безопасно исполнять кастомную логику своих клиентов (**тенантов**, мерчантов магазина) в заранее определённых точках расширения (**хуках**, например `checkout.discount`). Тенант загружает скомпилированный WebAssembly-модуль. Платформа проверяет его, исполняет в песочнице с лимитами времени и памяти и возвращает оператору типизированный исход: `ok`, `no_handler`, `timeout` и другие. Что делать при отказе, решает оператор в коде: например, применить скидку по умолчанию. Ближайшие аналоги — Shopify Functions и Extism.

Платформа делится на две части. **Data plane** (Go, тимлид) стоит на пути вызова и работает в масштабе миллисекунд. **Control plane** (Python, ваша зона) решает, что разрешено. Он хранит хуки, тенантов, ключи, модули и привязки и отдаёт data plane **снимок конфигурации**. Control plane не стоит на пути вызова: если он упал, магазин продолжает работать по последнему снимку, недоступны только консоли.

### Схема M1

```text
                 ┌────────────────────────── docker compose ──────────────────────────┐
 оператор ─────► │ control-plane :8000 (FastAPI)                                      │
 (curl, CLI)     │   /api/v1/...          API оператора    (ключ установки)           │
 тенант ───────► │   /api/v1/tenant/...   API консоли тенанта (JWT тенанта)           │
                 │   /internal/v1/config/snapshot   (внутренний токен) ◄────────┐     │
                 │        │ SQL                     │ S3 PUT                    │     │
                 │        ▼                         ▼                           │     │
                 │   PostgreSQL                MinIO  bucket modules            │     │
                 │                                  ▲ S3 GET по хешу            │     │
                 │ dataplane (Go)  [в compose с PR2]│                           │     │
                 │   :8080 публичный   /v1/invoke ◄──── магазин, curl           │     │
                 │   :8081 внутренний  /internal/v1/modules/validate ◄── CP     │     │
                 │   HTTPSource: long-poll снимка ──────────────────────────────┘     │
                 └────────────────────────────────────────────────────────────────────┘
```

- **Изменение конфигурации.** API меняет строки и в той же транзакции увеличивает версию снимка (`bump_config_version`). Data plane держит long-poll на `/internal/v1/config/snapshot` и получает новый снимок не позже чем через ~0,5 с, без перезапуска.
- **Модуль.** Загрузка (T3) кладёт байты в MinIO по ключу `<sha256-hex>.wasm`. Валидация (T4) просит data plane проверить модуль: data plane сам скачивает его по хешу. Активация (T5) меняет привязку, дальше работает обычный путь изменения конфигурации.
- **Сейчас (PR1)** в compose есть PostgreSQL, MinIO и control plane. Сервис `dataplane` добавляется следующим PR (PR2). До него Validate из compose недоступен.

### Кто за что отвечает

Кто кого вызывает и по какому контракту:

```text
 вызывающий                           контракт                               вызываемый
 web/ консоли (operator, tenant) ───► api/control-plane.openapi.yaml ──────► control-plane/  T1–T6
 web/ витрина (storefront) ─────────► api/demo-shop.openapi.yaml (нет) ────► demo-shop/      T7
 demo-shop/ через sdk/python/ (T8) ─► api/invoke.openapi.yaml ─────────────► dataplane :8080
 control-plane/ (dpclient, T4) ─────► api/dataplane-internal: Validate ────► dataplane :8081
 dataplane (HTTPSource) ────────────► api/dataplane-internal: снимок ──────► control-plane/
```

- **Ваша зона:** `control-plane/`, `demo-shop/`, `sdk/python/`. Вы владеете контрактами `api/control-plane.openapi.yaml` (генерируется из кода) и `api/demo-shop.openapi.yaml` (появится в T7).
- **Тимлид:** `dataplane/`, контракты `api/invoke.openapi.yaml` и `api/dataplane-internal.openapi.yaml`. В M1 он же сделал эндпоинт снимка и аутентификацию (решение D17), поэтому `configstate/` и `auth/` меняются только вместе с ним. Импортировать из них можно и нужно.
- **Фронтенд:** `web/`. Работает на моках MSW (`web/src/mocks/handlers.ts`) и переключает экраны на настоящий API по мере готовности T1–T6. Он ревьюер всех изменений контракта консолей. Его задачи F1–F9 и точки синхронизации с вашими — в `docs/handoff/frontend.md`.

### Что уже сделано и что делаете вы

Всё в `control-plane/src/controlplane/`, если не сказано иное.

| Часть | Где | Что это даёт вам |
|---|---|---|
| Каркас | `app.py`, `settings.py`, `db.py`, `errors.py`, `cli.py`; `control-plane/Dockerfile`, `compose.yaml`, CI-джоба `control-plane` | `create_app()` — единственное место сборки зависимостей. Ошибки отдаются как problem+json. CI гоняет ruff, mypy, pytest и проверяет дрейф контракта |
| Модель данных | `tables.py`, `migrations/versions/0001_initial.py` | Все семь таблиц M1 с ограничениями. Для T1–T6 новые миграции не нужны |
| Снимок | `configstate/` | `bump_config_version`, `VersionWatcher`, сборка снимка, long-poll эндпоинт |
| Аутентификация | `auth/` | `OperatorAuth` (ключ установки), `TenantAuth` (JWT тенанта), `InternalAuth`; `POST /api/v1/tenants/{external_id}/console-sessions`; CLI `apikey create` |
| **Эталонный срез** | `hooks/`, `tests/test_hooks.py` | Образец, который вы копируете (раздел 4) |
| **Заглушки** | `tenants/`, `apikeys/`, `modules/`, `bindings/`, `tenantconsole/` | Схемы запросов и ответов готовы и уже стоят в контракте. Эндпоинты отвечают 501. **Их реализация — ваши T1–T6** |
| Хранилище модулей | `storage/s3.py` | `ModuleStorage.put_module` / `get_module` (для T3) |
| Клиент Validate | `dpclient/` | `DataPlaneClient.validate`, `ValidationReport` (для T4) |
| Dev-инструменты | `devtools/`, CLI `seed-demo`, `validate-module` | Только для разработки, обходят API. Нужен data plane (PR2) |
| Тестовая обвязка | `control-plane/tests/conftest.py`, `tests/factories.py` | PostgreSQL и MinIO в testcontainers; фикстуры `engine`, `settings`, `client`, `minio`; фабрики строк |

Вы делаете T1–T8 по порядку из раздела 6. T1–T6 превращают заглушки в работающий API и закрывают PoC control plane. T7 (демо-магазин) и T8 (Python SDK) — следующие. Сроки задач определяет тимлид.

---

## 2. User stories

Кратко, полные тексты — в спеках. «Закрывают» — какие задачи делают историю правдой.

### Основной спек, §3.1–3.3

**3.1 Оператор подключает платформу.** Разработчик магазина разворачивает платформу и создаёт хук `checkout.discount` со схемами входа и выхода, лимитами и разрешениями. Получает ключ, подключает SDK и пишет в чекауте один вызов с дедлайном и скидкой по умолчанию 0. Пока ни один тенант не загрузил скрипт, платформа отвечает `no_handler`, и магазин работает как раньше.

*Закрывают:* хуки оператора (готово), ключи через API — **T2**, SDK — **T8**, чекаут магазина — **T7**. Консоль оператора делает фронтенд. Helm — MVP.

**3.2 Тенант кастомизирует скидку.** Мерчант видит в консоли хуки со схемами и примером входа. Пишет на Rust функцию «если прошлые покупки больше порога из конфига — скидка 10 %», загружает wasm, видит отчёт валидации, задаёт порог в конфиге и активирует версию. Следующий чекаут считает скидку его скриптом.

*Закрывают:* просмотр хуков — **T6**, загрузка — **T3**, валидация и отчёт — **T4**, конфиг и активация — **T5**. Playground — MVP. Шаблон на Rust уже есть в `examples/scripts/rust/discount`.

**3.3 Тенант выкатывает сломанную версию и откатывается.** Вторая версия зацикливается на пустой корзине, но проходит валидацию и активируется. Data plane убивает её по дедлайну, магазин получает `timeout` и применяет скидку по умолчанию. Мерчант откатывается на первую версию одним действием.

*Закрывают:* загрузка и активация второй версии — **T3–T5**, откат — **T5**. `timeout` уже обрабатывает data plane (M0). Circuit breaker, логи и метрики в консоли — MVP.

### Спек M1, §4.1–4.5

**4.1 Оператор разворачивает платформу и создаёт хук.** `docker compose up`, первый ключ через CLI, `POST /api/v1/hooks`. Меньше чем через секунду вызов меняется с `404` на `no_handler` без перезапуска data plane.

*Закрывают:* тимлид (готово в PR1, data plane в compose — PR2). Ключи через API — **T2**.

**4.2 Control plane падает, магазин не замечает.** Data plane держит последний снимок и повторяет long-poll с бэкоффом. Вызовы проходят, консоли недоступны.

*Закрывают:* тимлид (PR2). От вас нужно одно: не нарушать правила раздела 5, чтобы снимок оставался корректным.

**4.3 Мерчант открывает консоль из магазина.** Бэкенд магазина с ключом установки получает токен через `console-sessions`. Консоль тенанта видит только хуки и модули своего тенанта.

*Закрывают:* выдача и проверка токена (готово), эндпоинты консоли — **T3–T6**, кнопка «Мои скрипты» — **T7**.

**4.4 Backend-разработчик начинает работу.** Вы открываете этот документ, поднимаете compose, запускаете `uv run pytest`, читаете `controlplane/hooks/` по разделу 4 и делаете T1 по образцу. Критерии приёмки говорят, какие тесты должны появиться, CI подтверждает.

*Закрывают:* этот документ и **T1**.

**4.5 Модуль из MinIO (желательно).** `controlplane seed-demo` в обход API кладёт `discount.wasm` в MinIO, проверяет его через Validate, создаёт тенанта, модуль и привязку. Вызов возвращает `ok` со скидкой.

*Закрывают:* тимлид (dev-only, PR2). Настоящий поток загрузки, проверки и активации — **T3–T5**.

---

## 3. Как запустить

Команды даны для bash. На Windows используйте Git Bash: синтаксис `VAR=... команда`, heredoc и перенаправление `>` в PowerShell работают иначе.

### Что нужно

- **Docker Desktop**, запущенный. Тесты сами поднимают PostgreSQL (`postgres:17`) и MinIO (`chainguard/minio`) через testcontainers. Проверка: `docker info` должна ответить без ошибки. Без Docker тесты падают, а не пропускаются.
- **Python 3.13** и **[uv](https://docs.astral.sh/uv/getting-started/installation/)**. Если `uv` не в PATH после установки через pip, вызывайте `python -m uv` вместо `uv`.

### Тесты и линтеры

```bash
cd control-plane
uv sync                      # зависимости, включая dev-группу
uv run pytest                # первый запуск скачивает образы, это дольше
uv run ruff format . && uv run ruff check . && uv run mypy
```

CI-джоба `control-plane` выполняет то же самое (`ruff format --check`) и дополнительно проверяет, что `api/control-plane.openapi.yaml` совпадает с кодом (раздел 5).

### Стенд в compose

Из корня репозитория:

```bash
docker compose up -d --build --wait control-plane
curl -s localhost:8000/readyz        # {"status":"ready"}
```

Всегда называйте долгоживущий сервис (`control-plane`). Одноразовые `minio-init` (создаёт бакет) и `control-plane-migrate` (`controlplane migrate`) завершаются, и `--wait` по всем сервисам на некоторых версиях compose считает это ошибкой. Значения по умолчанию лежат в `compose.yaml`, переопределения — в `.env` по образцу `.env.example`. Остановить и стереть данные: `docker compose down -v`.

Наружу опубликованы `127.0.0.1:8000` (control plane), `127.0.0.1:5432` (PostgreSQL), `127.0.0.1:9000` (S3 API MinIO, для control plane, запущенного локально) и `127.0.0.1:9001` (консоль MinIO, логин `minioadmin` / `minioadmin`).

### Первый ключ установки

```bash
KEY=$(docker compose exec -T control-plane controlplane apikey create --name dev)
echo "$KEY"                          # whk_...; второй раз его не покажут
```

Токен печатается в stdout отдельной строкой, а строка `created key key_... (dev)` уходит в stderr. Флаг `-T` нужен, чтобы при захвате в переменную они не смешались. Имя ключа уникально: второй `--name dev` упадёт, берите другое имя.

### Первый хук

Сохраните тело в файл (в репозиторий его не коммитьте):

```bash
cat > hook.json <<'EOF'
{
  "name": "checkout.discount",
  "input_schema": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "required": ["cart_total", "customer"],
    "properties": {
      "cart_total": {"type": "number"},
      "customer": {
        "type": "object",
        "required": ["id", "lifetime_spend"],
        "properties": {
          "id": {"type": "string"},
          "lifetime_spend": {"type": "number"}
        }
      }
    }
  },
  "output_schema": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "required": ["discount_percent", "reason"],
    "properties": {
      "discount_percent": {"type": "integer", "minimum": 0, "maximum": 100},
      "reason": {"type": "string"}
    }
  },
  "timeout_ms": 50,
  "memory_max_pages": 64,
  "allowed_host_functions": [],
  "allowed_effect_types": [],
  "sample_input": {"cart_total": 120.5, "customer": {"id": "c-1", "lifetime_spend": 1500}}
}
EOF

curl -s -X POST localhost:8000/api/v1/hooks \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  --data @hook.json
```

Ответ `201` с `"def_version": 1`. Повтор того же запроса даёт `409` problem+json. Определение совпадает с хуком из тестов (`tests/factories.py`) и подходит для `discount.wasm`. Из PowerShell: `curl.exe`, аргумент `--data "@hook.json"` берите в кавычки.

### Снимок глазами data plane

```bash
curl -s -H "Authorization: Bearer dev-internal-token-change-me" \
  localhost:8000/internal/v1/config/snapshot
```

В ответе `version`, ключ `dev` (только sha256), хук `checkout.discount`. Long-poll: `?after_version=<текущая>&wait_s=30` висит до 30 с и возвращает `304`, если ничего не изменилось. Если за это время создать или изменить хук, вернётся `200` с новым снимком. Внутренний эндпоинт виден с localhost только потому, что dev-compose публикует порт 8000. В настоящей установке `/internal/*` наружу не выставляется.

### Локальный uvicorn против PostgreSQL из compose

Удобно для отладки с `--reload`. Если стенд уже поднят, сначала `docker compose stop control-plane`: порт 8000 занят.

```bash
docker compose up -d --wait postgres minio
cd control-plane
export CP_DATABASE_URL=postgresql+asyncpg://wasmhooks:wasmhooks@localhost:5432/controlplane
export CP_INTERNAL_TOKEN=dev-internal-token-change-me
export CP_TENANT_JWT_SECRET=dev-tenant-jwt-secret-change-me-0123456789
uv run controlplane migrate          # идемпотентно
uv run uvicorn --factory controlplane.app:create_app_from_env --reload
```

Настройки читаются только из переменных окружения с префиксом `CP_`: файл `.env` control plane не читает. `CP_INTERNAL_TOKEN` (не короче 16 символов) и `CP_TENANT_JWT_SECRET` (не короче 32 символов) обязательны даже для `migrate`. Остальные имеют значения по умолчанию, см. `settings.py`. S3 API MinIO опубликован на `localhost:9000` (значение `CP_S3_ENDPOINT` по умолчанию), бакет `modules` создаёт сервис `minio-init`: `docker compose up -d --wait postgres minio` и `docker compose run --rm minio-init` перед первой загрузкой.

### Что не работает до PR2

- `controlplane seed-demo` и `controlplane validate-module` вызывают Validate, а сервиса `dataplane` в compose ещё нет. До PR2 тенанта в compose через API можно создать только после T1, поэтому T1 первая.
- T4 разрабатывается против фейкового data plane в тестах (`httpx.MockTransport`, как в `tests/test_dpclient.py`).
- E2E-проверки T5 и T7 («вызов вернул `ok`») проходят после PR2.

---

## 4. Эталонный срез: хуки оператора

`hooks/` — образец, по которому делаются все остальные эндпоинты. Он реализует `GET/POST /api/v1/hooks` и `GET/PUT /api/v1/hooks/{name}`. Разберём путь `POST /api/v1/hooks`.

### Путь запроса

```text
POST /api/v1/hooks   Authorization: Bearer whk_...
  │
  ├─ OperatorAuth            auth/deps.py         нет ключа или он отозван → 401
  ├─ HookCreate              hooks/schemas.py     тело не по схеме → 422 (pydantic)
  ▼
create_hook                  hooks/router.py      без логики: тело → сервис → 201
  ▼
HookService.create           hooks/service.py
  ├─ normalized, check_hook_spec   hooks/rules.py      неверная JSON Schema → 422
  └─ async with engine.begin() as conn:                ── одна транзакция ──
       ├─ repo.insert_hook(conn, ...)    hooks/repo.py         имя занято → 409
       └─ bump_config_version(conn, ...) configstate/version.py  версия снимка +1
     commit
  ▼
VersionWatcher (≤ 0,5 с) будит long-poll → data plane получает снимок с новым хуком
```

### По файлам

**`hooks/schemas.py`** — формы запроса и ответа. Они же попадают в контракт, поэтому описания полей пишутся для фронтенда.

```python
class HookSpec(BaseModel):
    """Everything about a hook except its name: the body of PUT."""

    model_config = ConfigDict(extra="forbid")

    # ...
    timeout_ms: int = Field(ge=1, le=30000, description="Time limit of one call.")
    # ...


class HookCreate(HookSpec):
    """The body of POST: a spec plus the hook's name."""

    name: str = Field(pattern=HOOK_NAME_PATTERN, max_length=128)
```

**`hooks/router.py`** — только HTTP: путь, статус, модель ответа. Роутер строится функцией `build_router(...)` и получает готовый сервис и зависимость аутентификации.

```python
def build_router(service: HookService, operator: OperatorAuth) -> APIRouter:
    router = APIRouter(prefix="/api/v1/hooks", tags=["hooks"], dependencies=[Depends(operator)])

    # ...
    @router.post("", status_code=201, response_model=Hook, summary="Создать хук")
    async def create_hook(body: HookCreate) -> Hook:
        return await service.create(body)
```

**`hooks/service.py`** — логика. Сначала проверка входа, затем одна транзакция: записать и, если изменение видно в снимке, вызвать `bump_config_version` ровно один раз. `ProblemError` внутри `async with engine.begin()` откатывает транзакцию.

```python
    async def create(self, data: HookCreate) -> Hook:
        spec = normalized(HookSpec(**data.model_dump(exclude={"name"})))
        check_hook_spec(spec)
        async with self._engine.begin() as conn:
            row = await repo.insert_hook(conn, data.name, spec)
            if row is None:
                raise ProblemError(409, "Хук с таким именем уже есть", data.name)
            await bump_config_version(conn, "hook.created", {"name": row.name, "def_version": 1})
        return _to_hook(row)
```

В том же файле `replace` показывает изменение существующей строки. Строка читается с блокировкой (`get_hook(conn, name, for_update=True)`), чтобы два одновременных PUT не потеряли изменение. Если ничего не изменилось, сервис отвечает без роста версий.

**`hooks/repo.py`** — SQL на SQLAlchemy Core. Функции принимают соединение вызывающего, поэтому границы транзакции решает сервис. Возвращают frozen dataclass `HookRow`, а не `Row`. Конфликт уникальности обрабатывается без исключения: `ON CONFLICT DO NOTHING` и `None` в ответ.

```python
async def insert_hook(conn: AsyncConnection, name: str, spec: HookSpec) -> HookRow | None:
    """Inserts a hook at def_version 1; None if the name is taken."""
    query = (
        insert(hooks)
        .values(name=name, def_version=1, **_spec_values(spec))
        .on_conflict_do_nothing(index_elements=[hooks.c.name])
        .returning(*hooks.c)
    )
    m = (await conn.execute(query)).mappings().first()
    return None if m is None else _to_row(m)
```

**`configstate/version.py`** — версия снимка. Счётчик в одной строке `config_state` увеличивается под блокировкой строки. Блокировка держится до коммита, поэтому версии выдаются в порядке коммитов (решение D20), а вызывать функцию нужно последним запросом транзакции. Каждый вызов пишет строку аудита в `config_changes`.

```python
async def bump_config_version(conn: AsyncConnection, kind: str, payload: Mapping[str, Any]) -> int:
    # ...
    result = await conn.execute(
        update(config_state)
        .where(config_state.c.id == 1)
        .values(version=config_state.c.version + 1)
        .returning(config_state.c.version)
    )
```

**`tests/test_hooks.py`** — тесты через HTTP на настоящем PostgreSQL. Фикстура `client` (conftest.py) поднимает приложение целиком, фикстура `engine` очищает базу перед каждым тестом. Ключ создаётся готовой функцией:

```python
@pytest.fixture
async def auth(engine: AsyncEngine) -> AsyncIterator[dict[str, str]]:
    async with engine.begin() as conn:
        key = await create_api_key(conn, "operator")
    yield {"Authorization": f"Bearer {key.token}"}
```

Тест проверяет ответ, рост версии снимка ровно на 1 и то, что хук виден в снимке:

```python
async def test_create_hook(
    client: httpx.AsyncClient, engine: AsyncEngine, auth: dict[str, str]
) -> None:
    before = await snapshot_version(engine)
    resp = await client.post(URL, json=hook(), headers=auth)
    assert resp.status_code == 201, resp.text
    # ...
    assert await snapshot_version(engine) == before + 1
```

**`app.py`** — подключение. Сервис собирается здесь, роутер получает его готовым:

```python
    app.include_router(hooks_router.build_router(HookService(engine), operator_auth))
    # Stubs answering 501 until the backend tasks T1-T6 implement them.
    app.include_router(tenants_router.build_router(operator_auth))
```

### Рецепт: новый эндпоинт за 7 шагов

На примере T1. Заглушки уже содержат схемы и роутер, поэтому часть шагов — правка, а не создание.

1. **Схема.** Уже есть: `tenants/schemas.py`. Формы — это контракт, фронтенд по ним работает. Меняете форму — меняете контракт (раздел 5).
2. **Тест.** `tests/test_tenants.py` по образцу `tests/test_hooks.py`: по тесту на каждый критерий приёмки задачи. Сначала тесты красные (заглушка отвечает 501).
3. **Репозиторий.** `tenants/repo.py`: функции `async def f(conn: AsyncConnection, ...)` на `select` / `insert` / `update`, возвращают frozen dataclass (`TenantRow`). Дубликат — через `on_conflict_do_nothing(...).returning(...)`, как `insert_hook`.
4. **Сервис с транзакцией.** `tenants/service.py`: `class TenantService` получает `engine` в конструкторе. Чтение — `async with self._engine.connect()`, запись — `async with self._engine.begin()`. Ошибки — `ProblemError(status, "Заголовок по-русски", detail)`.
5. **`bump_config_version`**, если изменение видно в снимке: ровно один раз, в той же транзакции, последним запросом перед коммитом (почему — раздел 5, п. 2). `kind` — `"<сущность>.<действие>"` (`"tenant.created"`, `"tenant.updated"`). `payload` — идентификаторы, без секретов: `config_changes` — журнал аудита.
6. **Роутер.** В `tenants/router.py` `build_router(operator)` становится `build_router(service, operator)`. `raise NotImplementedError("T1")` заменяется вызовом сервиса. Строку «Реализуется в T1…» (`_NOTE`) из `description` уберите или замените описанием поведения.
7. **Подключение в `app.py`.** `tenants_router.build_router(operator_auth)` становится `tenants_router.build_router(TenantService(engine), operator_auth)`.

Чтобы закрыть задачу:

- удалите строки задачи из `ENDPOINTS` в `tests/test_stubs.py`: тест там ждёт 501. Проверки «без ключа — 401» и, для эндпоинтов тенанта, «ключ оператора — не токен тенанта» перенесите в свой тестовый файл;
- перегенерируйте контракт: `uv run controlplane openapi > ../api/control-plane.openapi.yaml` (из `control-plane/`, в Git Bash) и закоммитьте его вместе с кодом;
- `uv run ruff format . && uv run ruff check . && uv run mypy && uv run pytest` — всё зелёное.

### Подводные камни

- **`connect()` и `begin()`.** `engine.connect()` годится для чтения. `engine.begin()` — для записи: коммит на выходе из блока, откат при исключении. Запись внутри `connect()` молча откатится.
- **`IntegrityError` ловится снаружи блока `async with engine.begin()`.** Внутри транзакция после ошибки уже сломана. Где можно, обходитесь без исключений: при вставке — `ON CONFLICT DO NOTHING`, при изменении — чтение строки с `for_update` и проверка в той же транзакции. Ограничение БД остаётся страховкой.
- **Снимок в тестах кешируется.** `SnapshotService` отдаёт закешированный снимок, пока `VersionWatcher` не заметит новую версию (в тестах интервал опроса 0,05 с, в compose 0,5 с). Если тест читает снимок до и после изменения, второе чтение может вернуть старый снимок. Делайте второе чтение long-poll'ом (`?after_version=<версия до изменения>&wait_s=5`) или проверяйте версию через `config_state`, как `snapshot_version()` в `test_hooks.py`. То же касается `curl` руками: подождите полсекунды.
- **Идентификаторы.** В схемах ответов `id` модуля — строка, в таблице — UUID. Строку из запроса разбирайте через `uuid.UUID(...)` и превращайте `ValueError` в 404, а не в 500.
- **ORM не используется**, поэтому нет ленивой загрузки — главного источника `MissingGreenlet` в async SQLAlchemy. Пусть так и остаётся.
- **Схемы хуков строже, чем допускает Python.** Data plane компилирует схемы на Go (регулярные выражения RE2, без сети), поэтому `hooks/rules.py` пропускает только draft 2020-12, `$ref` только внутри схемы и на существующую цель, `pattern` без lookaround и обратных ссылок. Схема, которую Python принял, а Go не скомпилировал, сделала бы каждый вызов хука `unavailable`. Если где-то ещё принимаете JSON Schema, используйте `check_hook_spec` или её части.
- **`DataPlaneClient` отдаёт только два исключения.** Любая сетевая ошибка (обрыв соединения посреди ответа, сброс, таймаут) и 503 — `DataPlaneUnavailable`. Любой другой не-200 или испорченный отчёт — `DataPlaneError`. Других исключений из `validate()` не бывает, на этом держится T4.

---

## 5. Правила

1. **Контракт первым** (`docs/TEAM.md`). Формы всех эндпоинтов T1–T6 уже в `api/control-plane.openapi.yaml`, и фронтенд работает по ним. Изменение формы — это изменение контракта: PR, фронтенд в ревьюерах. Внутри `/v1` изменения только добавляющие.
2. **Каждая запись, видимая в снимке, вызывает `bump_config_version` ровно один раз, в той же транзакции и последним запросом перед коммитом.** Функция блокирует строку `config_state` до коммита. Возьмёте блокировку рано — все остальные писатели будут ждать всю вашу транзакцию. Возьмёте её до блокировки строки, которую держит другой писатель, — получите взаимоблокировку. В снимке видны ключи установки, хуки, тенанты (`external_id` и квоты) и привязки (`read_snapshot` берёт неотозванные ключи и привязки с активным модулем). Не видны имя тенанта, модули и их статусы: их изменение версию не двигает. Нет фактического изменения — нет вызова. Пропущенный вызов означает, что data plane не узнает об изменении до следующего. Лишний вызов ломает тесты «ровно на 1».
3. **Никакого сетевого I/O внутри транзакции записи.** Пока транзакция ждёт S3 или Validate, она держит блокировки строк и соединение из пула. Обращения к ним — до или после транзакции.
4. **Ошибки — `ProblemError`** (`errors.py`), а не `HTTPException` и не свой `JSONResponse`. Заголовок (`title`) по-русски, как в моках фронтенда, в `detail` — подробности. Коды: 400 — параметры или логически пустой запрос, 401 — аутентификация (уже сделано), 404, 409 — конфликт состояния, 413 — размер, 422 — тело не по схеме (pydantic делает сам).
5. **Тесты на настоящем PostgreSQL** (testcontainers, фикстуры из `conftest.py`). Без SQLite и моков базы. Data plane в тестах подменяется (`httpx.MockTransport`). Данные, которые API ещё не умеет создавать, — через `tests/factories.py`.
6. **Без тимлида не менять** `configstate/`, `auth/` и `api/dataplane-internal.openapi.yaml`. Модель данных (`tables.py`, `migrations/`) полная. Если понадобится новая колонка или миграция, сначала обсудите с тимлидом. Тест `test_migration_matches_tables` сверит миграцию с `tables.py`.
7. **После изменения схем или роутеров** — `uv run controlplane openapi > ../api/control-plane.openapi.yaml`. CI генерирует файл заново и падает при расхождении. Руками файл не правится.
8. **Стиль — явный, без магии.** SQLAlchemy Core (`Table`, `select`, `insert`, `update`), без ORM-классов и `relationship`. Транзакции видны в коде. Репозитории — функции от `conn`, возвращают frozen dataclass или pydantic-модель, а не `Row`. Сервисы получают зависимости в конструкторе. Роутеры строятся `build_router(...)`, связывание — только в `app.py`. Никаких изменяемых глобальных переменных. Типы везде, `mypy --strict`, ruff.
9. **`devtools/` — только для разработки.** Продуктовый код его не импортирует. Читать как пример можно.
10. **Одна задача — один PR.** Тимлид в ревьюерах, для T3–T6 ещё и фронтенд. CI зелёный на самом PR.

---

## 6. Задачи T1–T8

### Порядок

```text
 T1 тенанты ──► T2 ключи                                   API оператора
     │
     ├──► T6 чтение для консоли   (можно раньше по просьбе фронтенда)
     │
     └──► T3 загрузка ──► T4 валидация ──► T5 активация, откат, конфиг
                                                │          консоль тенанта
                                                ▼
                                    T7 демо-магазин ──► T8 Python SDK
```

Сроки задач определяет тимлид. Каждая задача закрыта, когда выполнены все её критерии, тесты из пункта «Тесты» есть и зелёные, строки задачи удалены из `tests/test_stubs.py`, контракт перегенерирован и CI зелёный.

Общее для консоли тенанта (T3–T6): тенант определяется по `TenantPrincipal.external_id` из JWT (`TenantAuth`). Тенант видит только свои модули и привязки. Чужие для него не существуют (404), а не запрещены (403). Хуки установки видны всем тенантам.

### Жизненный цикл модуля (T3–T5)

```text
 POST .../modules (T3)       фоновый валидатор (T4)                        PUT .../binding (T5)
 ─────────────────────       ──────────────────────                        ────────────────────
 байты ─► MinIO <hex>.wasm
 строка ─► uploaded ─► validating ─► Validate (data plane) ─┬─► validated ─► активация
                        ▲    │                              └─► rejected         │
                        │    └─ 503 / нет связи: повтор с бэкоффом,              ▼
                        │       статус остаётся validating                 снимок +1 ─► data plane
                        └─ рестарт control plane: uploaded и validating снова в очередь

 версия снимка: T3 и T4 её не меняют, T5 — +1 на каждое изменение привязки
```

«Активна» — свойство привязки (`bindings.active_module_id`), а не статус модуля (решение D14 спека M0). Статус `archived` в M1 не используется.

---

### T1. Тенанты и квоты

**Цель.** Оператор заводит тенантов и меняет их квоты через API. Тенант появляется в снимке. Это ближайшая копия среза хуков. Кроме того, до PR2 без неё в compose не создать тенанта через API, а значит, не выписать токен консоли для ручной проверки T3–T6.

**Эндпоинты** (заглушки `tenants/router.py`, схемы `tenants/schemas.py`, ключ установки): `GET /api/v1/tenants`, `POST /api/v1/tenants`, `GET /api/v1/tenants/{external_id}`, `PATCH /api/v1/tenants/{external_id}`.

**Критерии приёмки.**

- [ ] `POST` создаёт тенанта и отвечает `201` с `Tenant`. Квоты по умолчанию 0 (0 — значение по умолчанию data plane). Версия снимка +1. Тенант есть в снимке (`tenants[]`: `external_id`, `concurrency_limit`, `rate_limit_rps`).
- [ ] Дубликат `external_id` — `409` «Тенант с таким external_id уже есть», версия не меняется.
- [ ] `GET` списка — `{items: [...]}`, по `external_id`. `GET` одного — `200`. Неизвестный — `404` «Тенант не найден» (тот же заголовок, что у `console-sessions`).
- [ ] `PATCH` меняет только переданные поля (`null` или отсутствие — «не менять»). Строка читается с блокировкой (`with_for_update`), как в `HookService.replace`.
- [ ] Изменение квот — версия +1 ровно один раз, в снимке новые значения.
- [ ] Изменение только `name` — `200`, `updated_at` обновлён, версия снимка не меняется (имени в снимке нет).
- [ ] `PATCH` без фактических изменений (`{}` или те же значения) — `200`, версия и `updated_at` не меняются.
- [ ] Отрицательные квоты или пустой `external_id` — `422`.
- [ ] Без ключа или с токеном тенанта — `401`.
- [ ] `POST /api/v1/tenants/{external_id}/console-sessions` для созданного через API тенанта отвечает `201`.

**Не в объёме:** удаление тенанта, смена `external_id` (это идентичность тенанта в вызовах), применение квот в data plane (MVP), поле `status`.

**Тесты:** `tests/test_tenants.py` — по тесту на каждый пункт. Версия проверяется до и после, как `snapshot_version()` в `test_hooks.py`.

**Опора:** `hooks/` целиком; `tables.tenants`; `bump_config_version`; `tests/factories.py: add_tenant`.

---

### T2. Ключи установки

**Цель.** Оператор выпускает, видит и отзывает ключи через API (сейчас — только CLI). Ключ аутентифицирует и API оператора, и вызовы Invoke в data plane: data plane получает sha256 ключей в снимке.

**Эндпоинты** (заглушки `apikeys/router.py`, схемы `apikeys/schemas.py`, ключ установки): `GET /api/v1/api-keys`, `POST /api/v1/api-keys`, `DELETE /api/v1/api-keys/{key_id}`.

**Критерии приёмки.**

- [ ] `POST` — `201` с `ApiKeyCreated`. `token` (`whk_` + 32 символа base62) есть только в этом ответе, в БД хранится sha256.
- [ ] Создание идёт через готовую `auth.keys.create_api_key(conn, name)`. Она уже вызывает `bump_config_version`, второй вызов не нужен (иначе версия вырастет на 2).
- [ ] Версия снимка +1, ключ в снимке (`api_keys[]`: `id`, `name`, `sha256`). Новым ключом сразу проходит `GET /api/v1/hooks`.
- [ ] Дубликат имени — `409` «Ключ с таким именем уже есть». `create_api_key` делает обычный `INSERT`, поэтому проверьте имя в той же транзакции до вставки. `IntegrityError` (ловить снаружи `async with`) остаётся страховкой от гонки.
- [ ] `GET` — `{items: [...]}` со всеми ключами, включая отозванные (`id`, `name`, `created_at`, `revoked_at`). Ни `token`, ни `sha256` в ответе нет.
- [ ] `DELETE` — `204`, `revoked_at = now()`, версия +1 ровно один раз. Ключ пропадает из снимка, запрос с ним — `401`. `OperatorAuth` проверяет `revoked_at` на каждом запросе, дописывать ничего не нужно, но тест обязателен.
- [ ] Повторный `DELETE` того же ключа — `204` без изменения версии. Неизвестный `key_id` — `404` «Ключ не найден».
- [ ] Без ключа или с токеном тенанта — `401`.

**Не в объёме:** права ключей (один уровень, D19), ротация, физическое удаление строк. Отозвать свой или последний ключ можно, восстановление — `controlplane apikey create`.

**Тесты:** `tests/test_apikeys.py`. Образец отзыва — `test_revoked_key_rejected` в `tests/test_auth.py`.

**Опора:** `auth/keys.py` (`create_api_key`, `hash_token`); `tables.api_keys`; `snapshot.read_snapshot` (фильтр `revoked_at IS NULL`).

---

### T3. Загрузка модуля

**Цель.** Тенант загружает версию wasm-модуля для хука и видит список своих версий.

**Эндпоинты** (заглушки `modules/router.py`, схемы `modules/schemas.py`, JWT тенанта): `GET /api/v1/tenant/hooks/{hook}/modules`, `POST /api/v1/tenant/hooks/{hook}/modules` (multipart, поле `file`).

**Критерии приёмки.**

- [ ] `POST` — `201` с `Module`: `status: "uploaded"`, `validation_report: null`, `content_hash` = `sha256:<hex>` содержимого, `size_bytes`, `hook` (имя), `id` (UUID строкой), `uploaded_at`.
- [ ] Файл больше `CP_MAX_MODULE_BYTES` (`settings.max_module_bytes`, 10 МиБ) — `413` «Модуль слишком большой». Читать не больше лимита + 1 байт: `await file.read(limit + 1)`.
- [ ] Пустой файл — `422` (в таблице `size_bytes > 0`).
- [ ] Неизвестный хук — `404` «Хук не найден».
- [ ] Порядок: сначала `ModuleStorage.put_module(data)`, затем строка `modules` в транзакции. `put_module` идемпотентна (те же байты — тот же ключ `<hex>.wasm`). Объект без строки безвреден, строка без объекта сломала бы валидацию.
- [ ] Повторная загрузка тех же байт тем же тенантом для того же хука — `409` «Такая версия уже загружена», `detail` = хеш (как в моках). Тот же файл у другого тенанта — не дубликат (уникальность `(tenant_id, hook_id, content_hash)`).
- [ ] Загрузка версию снимка не двигает.
- [ ] `GET` — `{items: [...]}`: модули этого тенанта и этого хука, новые первыми (`uploaded_at` по убыванию). Неизвестный хук — `404`. Модули другого тенанта не видны.
- [ ] Без токена или с ключом оператора — `401`.

**Не в объёме:** валидация (T4, до неё модуль остаётся `uploaded`), удаление и архивирование версий, проверка «это wasm» (это проверка `compile` в Validate), ограничение размера до приёма тела (Starlette принимает тело целиком, лимит на прокси — MVP).

**Тесты:** `tests/test_modules.py` против MinIO из testcontainers.

**Опора:** `storage/s3.py` (`ModuleStorage.from_settings`, `put_module`, `module_hash`), `tests/test_storage.py`; `TenantAuth`.

**Подключение.** `create_app` создаёт `ModuleStorage.from_settings(settings)` и передаёт его в сервис вместе с `settings.max_module_bytes`. Фикстура `settings` в `conftest.py` не знает про MinIO. Для тестов T3 сделайте свою фикстуру: `Settings` с `s3_endpoint`, `s3_bucket`, `s3_access_key`, `s3_secret_key` из фикстуры `minio` и свой `client` поверх неё. Остальным тестам MinIO не нужен: boto3 при создании клиента никуда не подключается.

**Отчёт валидации в ответах.** В `ValidationCheck` поле `detail` для пройденных проверок не передаётся вовсе, а не приходит `null`. Так в контракте data plane, и так ждёт zod-схема фронтенда (`web/src/entities/module/model/schema.ts`). Это делает `exclude_if` в `dpclient/schemas.py`, тест — `tests/test_dpclient.py`. Сохраняйте отчёт через `report.model_dump(mode="json")`, и всё сойдётся.

---

### T4. Оркестрация валидации

**Цель.** Каждый загруженный модуль проверяется data plane в фоне. Тенант видит результат: `validated` или `rejected` с отчётом.

**Эндпоинтов нет.** Это фоновая задача в процессе control plane, результат виден в `GET .../modules` (T3).

**Критерии приёмки.**

- [ ] После успешного `POST` модуля его id ставится в очередь фонового валидатора. Ответ `POST` валидацию не ждёт (`201` со статусом `uploaded`).
- [ ] Валидатор переводит модуль в `validating` и коммитит. Затем вне транзакции вызывает `DataPlaneClient.validate(content_hash, hook, config)`. Результат записывается одной транзакцией: `validated`, если `report.ok`, иначе `rejected`; отчёт — в `validation_report` (`report.model_dump(mode="json", exclude_none=True)`, как в `devtools/seed.py`).
- [ ] `hook` — текущее определение хука как `HookDefOut` (`configstate/schemas.py`): `HookDefOut(name=row.name, def_version=row.def_version, **row.spec.model_dump())` из `hooks.repo.get_hook`. `config` — конфиг привязки этой пары тенант+хук, если она есть, иначе не передаётся.
- [ ] **Повторы при 503.** `DataPlaneUnavailable` (ответ 503, нет соединения, таймаут) — повтор с экспоненциальным бэкоффом и full jitter (например, от 0,5 до 30 с), статус остаётся `validating`. После успешного ответа — обычный переход.
- [ ] `DataPlaneError` (любой другой не-200, например 400 или 401) — ошибка нашего запроса или конфигурации, а не модуля. Пишется лог уровня error, повтор с тем же бэкоффом. `rejected` ставится только по отчёту data plane с `ok: false`.
- [ ] **Восстановление после рестарта.** При старте приложения (в `lifespan`) все модули в статусах `uploaded` и `validating` ставятся в очередь заново, старые первыми.
- [ ] Остановка приложения отменяет валидатор и закрывает клиент (`DataPlaneClient.aclose()`). Незавершённые модули остаются `validating` и подхватываются при следующем старте.
- [ ] Смена статуса и запись отчёта версию снимка не двигают.
- [ ] Один валидатор на процесс: data plane по умолчанию всё равно выполняет валидации по одной.

**Не в объёме:** несколько реплик control plane (захват строки через `FOR UPDATE SKIP LOCKED` — MVP), повторная валидация при изменении хука (раздел 7), ручной перезапуск валидации, ограничение числа попыток.

**Тесты:** `tests/test_validation.py`. Data plane подменяется `httpx.MockTransport` (как в `tests/test_dpclient.py`) или фейком (как `FakeClient` в `tests/test_devtools.py`). Случаи:

- `ok: true` → `validated` и отчёт;
- `ok: false` → `rejected` и отчёт;
- ответы 503, 503, 200 → три вызова, между ними статус `validating`, в конце `validated`;
- восстановление: строки в `uploaded` и `validating` через `factories.add_module(..., status=...)`, старт приложения → оба модуля в конечном статусе;
- версия снимка не изменилась.

Базовую задержку бэкоффа передавайте в конструктор, чтобы тесты не ждали секундами.

**Опора:** `dpclient/client.py` (`DataPlaneClient` с параметром `transport` для тестов, `DataPlaneUnavailable`, `DataPlaneError`), `dpclient/schemas.py` (`ValidationReport`), `configstate/watcher.py` как образец фоновой задачи со `start()` и `stop()` в `lifespan`.

**Подключение.** В `create_app`: `DataPlaneClient(settings.dataplane_internal_url, settings.internal_token.get_secret_value())` — тот же внутренний токен, что у снимка. Валидатор стартует и останавливается в `lifespan` рядом с `watcher`. Тестам нужен способ подменить клиент (например, необязательный параметр `create_app`). Иначе каждый тест приложения будет ходить в несуществующий `localhost:8081`, а модули из тестов T3 уйдут в бесконечные повторы.

**До PR2** сервиса `dataplane` в compose нет, вся разработка идёт против фейка. После PR2 проверка руками: `discount.wasm` → `validated`, `http-call.wasm` → `rejected` с проваленной проверкой `imports` (файлы — в `dataplane/testdata/wasm/`).

---

### T5. Активация, откат, конфиг

**Цель.** Тенант активирует проверенную версию, откатывается на прежнюю и задаёт конфиг привязки. Изменение доходит до data plane.

**Эндпоинт** (заглушка `bindings/router.py`, тело `bindings/schemas.py: BindingUpdate`, ответ `tenantconsole/schemas.py: TenantBinding`, JWT тенанта): `PUT /api/v1/tenant/hooks/{hook}/binding`, тело — `active_module_id` и/или `config`.

**Критерии приёмки.**

- [ ] Тело без обоих полей (`{}` или оба `null`) — **`400`** «Нечего менять». Заглушка сейчас такое тело принимает, проверку делает сервис.
- [ ] Лишнее поле — `422` (в схеме `extra="forbid"`).
- [ ] Неизвестный хук — `404` «Хук не найден».
- [ ] `active_module_id` должен указывать на существующий модуль этого тенанта и этого хука. Иначе — `404` «Версия не найдена», как в моках: чужой модуль для тенанта не существует. Не-UUID — тоже `404`.
- [ ] Модуль не в статусе `validated` — `409` «Активировать можно только проверенную версию», `detail` — `Статус: <status>` (как в моках).
- [ ] Проверки делает сервис. Составной внешний ключ `bindings → modules(id, tenant_id, hook_id)` — последняя линия обороны, а не основной механизм.
- [ ] Первое изменение создаёт строку `bindings` (`config_version` = 1): вставка через `on_conflict_do_nothing`, затем чтение строки с блокировкой (`with_for_update`), чтобы два одновременных первых `PUT` не упали на первичном ключе. Дальше — `UPDATE` прочитанной строки.
- [ ] Смена активного модуля `config_version` не меняет (§6.3 спека M1). Смена `config` увеличивает `config_version` на 1. Тот же `config` его не меняет.
- [ ] Каждое изменение привязки — версия снимка +1 ровно один раз, даже если в одном запросе меняются и модуль, и конфиг. Запрос без фактических изменений — `200` без роста версий.
- [ ] Конфиг без активной версии допустим: строка создаётся с `active_module_id = null` и попадает в снимок после активации.
- [ ] Ответ `200` с `TenantBinding`: `active_module_id`, `active_module_hash` (из `modules.content_hash`), `config`, `config_version`, `updated_at`.
- [ ] Снимок: привязка в `bindings[]` с `module_hash` активного модуля и `config`.
- [ ] Откат — тот же `PUT` с id прежней версии. Тест: активировать A, затем B, затем A — в снимке хеш A.
- [ ] Без токена или с ключом оператора — `401`.
- [ ] E2E после PR2: после активации `discount.wasm` вызов `checkout.discount` для тенанта возвращает `ok` со скидкой.

**Не в объёме:** снятие активной версии (деактивация), circuit breaker и автоматический откат (MVP), playground, повторная валидация с новым конфигом.

**Тесты:** `tests/test_bindings.py`. Модули нужных статусов готовятся через `factories.add_module(..., status=...)`: MinIO и data plane не нужны.

**Опора:** `tables.bindings`; `snapshot.read_snapshot` (как привязки попадают в снимок); `devtools/seed.py: _upsert_binding` — пример логики `config_version` (читать, не импортировать).

---

### T6. Чтение для консоли тенанта

**Можно раньше по просьбе фронтенда.** Задача не зависит от T3–T5: в тестах данные создаются фабриками.

**Цель.** Консоль тенанта показывает хуки оператора со схемами и примером входа и состояние привязки тенанта. Фронтенд переключает эти экраны с MSW на настоящий API.

**Эндпоинты** (заглушки `tenantconsole/router.py`, схемы `tenantconsole/schemas.py`, JWT тенанта): `GET /api/v1/tenant/hooks`, `GET /api/v1/tenant/hooks/{hook}`.

**Критерии приёмки.**

- [ ] `GET` списка — `{items: [{hook, binding}, ...]}` по всем хукам установки, по имени. `binding` — привязка этого тенанта или `null`.
- [ ] `hook` — `HookView`: поля определения хука без меток времени.
- [ ] `binding` — `TenantBinding`, как в T5. У привязки без активной версии `active_module_id` и `active_module_hash` равны `null`.
- [ ] Привязки других тенантов не видны (тест с двумя тенантами на одном хуке).
- [ ] `GET` одного — то же для одного хука. Неизвестный — `404` «Хук не найден».
- [ ] Формы совпадают с моками (`web/src/mocks/handlers.ts`) и zod-схемами (`web/src/entities/hook/model/schema.ts`). Заглушки уже им соответствуют, формы не меняйте.
- [ ] Версию снимка не трогает.
- [ ] Без токена или с ключом оператора — `401`.

**Не в объёме:** `/tenant/invocations` (MVP, фронтенд продолжает его мокать). CORS не нужен: консоли ходят в control plane через прокси Next.js (F2 в `docs/handoff/frontend.md`).

**Тесты:** `tests/test_tenant_console.py`, данные через `factories` (`add_hook`, `add_tenant`, `add_module`, `add_binding`).

**Опора:** `hooks/repo.py` (`list_hooks`, `get_hook`) — переиспользуйте; `TenantAuth`.

**Ручная проверка.** Токен консоли (тенант должен существовать — T1):

```bash
curl -s -X POST localhost:8000/api/v1/tenants/merchant-a/console-sessions -H "Authorization: Bearer $KEY"
```

---

### T7. Бэкенд демо-магазина

**Цель.** Демо-магазин — оператор платформы. Витрина (route group `(storefront)` в `web/`, решение D13 спека M0) ходит в его API. Чекаут вызывает хук `checkout.discount` и применяет скидку, которую посчитал скрипт мерчанта.

**Где:** `demo-shop/` (сейчас там только README). Контракт `api/demo-shop.openapi.yaml` пока не существует.

**Критерии приёмки.**

- [ ] Контракт первым: `api/demo-shop.openapi.yaml` появляется до кода, PR с фронтендом в ревьюерах. Удобнее всего генерировать его из FastAPI, как контракт консолей.
- [ ] FastAPI-приложение в `demo-shop/` со своим `pyproject.toml`, в стиле control plane (раздел 5, п. 8).
- [ ] Чекаут вызывает Invoke `checkout.discount` с ключом установки магазина и дедлайном. Вход — по `input_schema` хука: `cart_total`, `customer.id`, `customer.lifetime_spend`.
- [ ] Любой исход, кроме `ok` (`no_handler`, `timeout`, `handler_error`, `quota_exceeded`, `unavailable`, ошибка сети), даёт скидку 0. Чекаут не ломается.
- [ ] Кнопка «Мои скрипты»: бэкенд магазина вызывает `POST /api/v1/tenants/{external_id}/console-sessions` с ключом установки и отдаёт витрине `token` и `expires_at` для консоли тенанта.

**Не в объёме:** оплата и учётные записи покупателей. Детали каталога и корзины согласуются с фронтендом в контракте магазина.

**Тесты:** pytest с фейковым Invoke (`httpx.MockTransport`) на каждый исход. E2E против compose — после PR2.

**Опора:** `api/invoke.openapi.yaml`, спек M0 §6.1.3 (исходы и HTTP-статусы), `auth/router.py` (console-sessions). До T8 Invoke вызывается тонкой обёрткой на httpx, после T8 — через SDK.

---

### T8. Python SDK

**Цель.** Тонкий клиент Invoke для приложения оператора: один вызов с дедлайном и значением по умолчанию. Политика отказа живёт в точке вызова (решение 6 основного спека).

**Где:** `sdk/python/` (сейчас там только README). Контракт `api/invoke.openapi.yaml` (владелец — тимлид, SDK его только потребляет).

**Критерии приёмки.**

- [ ] Эскиз публичного API SDK (сигнатуры, типы исходов, поведение при ошибках оператора: 400, 401, 404, 413) — коротким PR на ревью тимлиду до кода.
- [ ] Дедлайн: `deadline_ms` уходит в запросе, таймаут HTTP-клиента не больше оставшегося дедлайна.
- [ ] Повтор при `unavailable` (503, скрипт не запускался) — только в пределах дедлайна. Остальные исходы не повторяются.
- [ ] Значение по умолчанию: при любом исходе, кроме `ok`, и при истёкшем дедлайне SDK возвращает значение по умолчанию оператора с `fallback=true`.
- [ ] Типизированные исходы: `ok`, `no_handler`, `timeout`, `handler_error`, `quota_exceeded`, `unavailable`. Результат и эффекты — при `ok`.
- [ ] Tolerant reader: незнакомые поля ответа игнорируются.
- [ ] Тесты против data plane из compose (после PR2). До PR2 — `httpx.MockTransport` или data plane в режиме M0 (`dataplane/README.md`, «Запуск бинарника вручную»).

**Не в объёме:** Go SDK (MVP), публикация пакета.

**Опора:** `api/invoke.openapi.yaml`, спек M0 §6.1.3, основной спек §3.1.

---

## 7. Ограничения M1, которые вас касаются

Из §14 спека M1. Это осознанные компромиссы, не баги.

- JWT тенанта отзывается только истечением срока (`CP_TENANT_TOKEN_TTL_S`, по умолчанию час).
- Один уровень прав у ключа установки: ключ в бэкенде магазина может менять хуки.
- Квоты есть в модели и снимке, но data plane их не применяет (MVP). T1 хранит и распространяет их.
- Изменение схем и лимитов хука не перепроверяет уже привязанные модули. Оператора защищает проверка выхода на горячем пути (`handler_error`).
- Восстановление БД из бэкапа может вернуть версию снимка назад, и data plane будет отвергать старые версии до перезапуска.

Вопросы — тимлиду (kudesn1k1). Расхождение этого документа с кодом — повод для PR в документ: прав код.
