# e2e: сценарий Milestone 1

Go-модуль `github.com/kudesn1k1/WasmHooks/e2e`, только стандартная библиотека. Прогоняет сценарий из спека M1 §8.2 против поднятого compose и доказывает утверждения вехи:

- изменение конфигурации в control plane доходит до data plane без перезапуска, быстрее чем за секунду;
- при остановленном control plane data plane обслуживает вызовы по последнему снимку и остаётся готовым;
- после возврата control plane data plane подхватывает следующее изменение;
- (желательно) модуль, положенный seed-командой в MinIO, исполняется, а Validate отвергает модуль с запрещённым импортом и принимает хороший.

## Запуск

Из корня репозитория поднять compose, затем из `e2e/`:

```bash
docker compose up -d --build --wait control-plane dataplane
cd e2e
go run ./cmd/demo                 # таблица, код выхода 1 при любом FAIL
E2E=1 go test -count=1 -v ./...   # те же шаги как тест; без E2E=1 тест пропускается
```

Флаги демо: `--compose-dir` (по умолчанию `..`, корень репозитория при запуске из `e2e/`), `--control-plane` (`http://localhost:8000`), `--data-plane` (`http://localhost:8080`), `--skip-should` (только шаги 1–4).

Шаги CLI control plane выполняются через `docker compose exec -T control-plane controlplane ...`, остановка и запуск control plane — через `docker compose stop|start control-plane`. Поэтому нужен `docker` в `PATH`, а compose должен быть поднят из того же каталога, что передан в `--compose-dir`.

## Шаги

Перед шагами сценарий ждёт `GET /readyz` data plane = 200 не дольше 30 с: на свежем compose data plane стартует раньше control plane и может быть в бэкоффе до 10 с. Не дождался — ошибка без таблицы.

| # | Действие | PASS, если |
|---|---|---|
| 1 | `apikey create`; вызовы хука раз в 50 мс до ответа, отличного от 401, не дольше 3 с | первый не-401 ответ — `404`: ключ дошёл, хука нет |
| 2 | `POST /api/v1/hooks` (схемы из `examples/demo/snapshot.json`); вызовы раз в 20 мс до смены ответа | `no_handler`, не позже 1 с после ответа `201` |
| 3 | `docker compose stop control-plane`; 20 вызовов; `GET /readyz` data plane | control plane не отвечает, все 20 — `no_handler`, `readyz` = 200 |
| 4 | `docker compose start control-plane`, ждать его `readyz`; `PUT` хука с новым обязательным полем `currency`; прежний payload раз в 100 мс не дольше 15 с | ответ стал `400` |
| 5* | `seed-demo` кладёт `discount.wasm` в MinIO и привязывает к тенанту; вызовы с `currency` и `lifetime_spend: 1500` до исхода, отличного от `no_handler`/`unavailable` | `ok`, `discount_percent == 10`, `module_hash` совпадает с хешем от `seed-demo` |
| 6* | `validate-module /fixtures/http-call.wasm` | `ok: false`, проверка `imports` провалена |
| 7* | `validate-module /fixtures/discount.wasm` | `ok: true` |

\* — уровень «желательно», `--skip-should` их пропускает.

Колонка TIME — время всего шага; ключевые замеры (время распространения, длительность `stop`, время подхвата после отказа) — в колонке ACTUAL. Если шаг оставляет систему непригодной для следующих (нет ключа, хук не создан, control plane не поднялся), остальные шаги выводятся как FAIL с пометкой `not run`. Если прогон прерван, пока control plane остановлен, сценарий запускает его обратно.

## Повторяемость

Каждый прогон берёт суффикс `r<unix-секунды в base36>`: хук `checkout.discount_<suffix>`, тенант `merchant-a-<suffix>`, ключ `e2e-<suffix>`. Сценарий проходит повторно на том же compose без `down -v` и не зависит от прошлых прогонов. Хуки, тенанты и ключи прошлых прогонов остаются в базе; удалить всё — `docker compose down -v`.

## CI

Джоба `e2e` в `.github/workflows/ci.yml`: `docker compose up -d --build --wait control-plane dataplane`, `go test` с `E2E=1`, при падении — `docker compose ps -a` и логи compose, в конце всегда `docker compose down -v`.

## Если упало

```bash
docker compose ps -a
docker compose logs dataplane control-plane
```

Data plane пишет `snapshot applied` с номером версии на каждый принятый снимок и `snapshot rejected or control plane unreachable` с `retry_in` на каждую неудачную попытку long-poll.
