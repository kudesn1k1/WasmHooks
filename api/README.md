# API-контракты

HTTP-контракты между компонентами WasmHooks описаны в OpenAPI 3.1, внутренний
gRPC-контракт между gateway и executor — в Protocol Buffers.

## Таблица контрактов

| Контракт | Файл | Владелец | Реализует | Потребляет |
|---|---|---|---|---|
| Публичный Invoke | `invoke.openapi.yaml` | тимлид (Go, data plane) | data plane | Python SDK, демо-магазин, любой оператор |
| Снимок конфигурации | `dataplane-internal.openapi.yaml` | тимлид (Go, data plane) | control plane | data plane |
| Валидация модуля | `dataplane-internal.openapi.yaml` | тимлид (Go, data plane) | data plane | control plane |
| Исполнение хука и готовность executor-а | `executor.proto` | тимлид (Go, data plane) | executor | gateway, discovery executor-ов |
| ABI скрипта | основной спек (`docs/superpowers/specs/2026-09-04-wasm-extension-platform-design.md`), не файл в `api/` | тимлид (Go, data plane) | data plane | авторы скриптов |
| API консолей | `control-plane.openapi.yaml` (M0: ещё не создан) | backend, Python | control plane | фронтенд |
| API магазина | `demo-shop.openapi.yaml` (генерируется из кода, `demoshop openapi`) | backend, Python | демо-магазин | витрина |

Источник таблицы — `docs/TEAM.md`; при расхождении прав он.

## Правила изменения контрактов

В M3 внутреннее исполнение между процессами описано в [executor.proto](executor.proto). Gateway вызывает Execute и Health по gRPC, control plane продолжает валидировать модули через внутренний HTTP API. Публичный Invoke различает подтверждённый отказ до запуска (503) и потерю результата исполнения после отправки запроса (502).

- Контракт меняет его владелец через PR. Потребители контракта — обязательные ревьюеры.
- Внутри версии (`/v1`) изменения только добавляющие: новые необязательные поля,
  новые эндпоинты, новые значения перечислений там, где это оговорено явно в
  описании поля. Удаление, переименование поля и смена типа означают новую
  версию (`/v2`).
- Клиенты — tolerant reader: игнорируют незнакомые поля ответа. Благодаря этому
  добавление поля никогда не ломает потребителей.
- Реализация не опережает контракт: сначала PR в `api/`, потом код, который под
  него подстраивается.
- Nullable-поля описываются через массив типов (`type: [object, "null"]`), как
  того требует JSON Schema 2020-12 / OpenAPI 3.1 — ключевое слово `nullable` из
  OpenAPI 3.0 не используется.

## Проверка

Контракты обязаны проходить lint без ошибок (warnings по `info.license` и
похожим необязательным полям допустимы):

```bash
npx @redocly/cli lint api/*.yaml
```

Для обновления Go-клиента и сервера после изменения `executor.proto` выполните
из корня проекта с `protoc`, `protoc-gen-go v1.36.11` и `protoc-gen-go-grpc v1.5.1` в `PATH`.

```bash
protoc -I api --go_out=dataplane --go-grpc_out=dataplane \
  --go_opt=module=github.com/kudesn1k1/WasmHooks/dataplane \
  --go-grpc_opt=module=github.com/kudesn1k1/WasmHooks/dataplane api/executor.proto
```

## Соответствие Go-структурам

`dataplane-internal.openapi.yaml` описывает `Snapshot`, `APIKey`, `HookDef`,
`Tenant`, `Binding` поле в поле так, как они объявлены в пакете `config`
data plane (JSON-теги, типы, обязательность). При изменении структур пакета
`config` контракт обновляется тем же PR.
