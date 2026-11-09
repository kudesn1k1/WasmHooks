# e2e: сценарий и генератор нагрузки

Go-модуль `github.com/kudesn1k1/WasmHooks/e2e`, только стандартная библиотека. Две программы против поднятого compose:

- `cmd/demo` — сценарий из спека M1 §8.2 и M2 §9.3;
- `cmd/loadgen` — генератор нагрузки «шумный тенант» (спек M2 §9.3, ниже).

Сценарий доказывает утверждения вех:

- изменение конфигурации в control plane доходит до data plane без перезапуска, быстрее чем за секунду;
- при остановленном control plane data plane обслуживает вызовы по последнему снимку и остаётся готовым;
- после возврата control plane data plane подхватывает следующее изменение;
- (желательно) модуль, загруженный тенантом через API, проходит фоновую валидацию, активируется и исполняется, а Validate отвергает модуль с запрещённым импортом и принимает хороший;
- (желательно, M2) квота тенанта, изменённая через `PATCH`, действует на data plane без перезапуска и снимается так же.

Запросы к API control plane (ключ оператора, хук, тенант, токен консоли, загрузка, активация, квота) собраны в `internal/cpapi`, их используют и сценарий, и генератор.

## Запуск

Из корня репозитория поднять compose, затем из `e2e/`:

```bash
docker compose up -d --build --wait control-plane dataplane
cd e2e
go run ./cmd/demo                 # таблица, код выхода 1 при любом FAIL
E2E=1 go test -count=1 -v ./...   # те же шаги как тест; без E2E=1 тест пропускается
```

Флаги демо: `--compose-dir` (по умолчанию `..`, корень репозитория при запуске из `e2e/`), `--fixtures` (`../dataplane/testdata/wasm`, каталог с `discount.wasm`), `--control-plane` (`http://localhost:8000`), `--data-plane` (`http://localhost:8080`), `--skip-should` (только шаги 1–4).

Шаги CLI control plane выполняются через `docker compose exec -T control-plane controlplane ...`, остановка и запуск control plane — через `docker compose stop|start control-plane`. Поэтому нужен `docker` в `PATH`, а compose должен быть поднят из того же каталога, что передан в `--compose-dir`.

## Шаги

Перед шагами сценарий ждёт `GET /readyz` data plane = 200 не дольше 30 с: на свежем compose data plane стартует раньше control plane и может быть в бэкоффе до 10 с. Не дождался — ошибка без таблицы.

| # | Действие | PASS, если |
|---|---|---|
| 1 | `apikey create`; вызовы хука раз в 50 мс до ответа, отличного от 401, не дольше 3 с | первый не-401 ответ — `404`: ключ дошёл, хука нет |
| 2 | `POST /api/v1/hooks` (схемы из `examples/demo/snapshot.json`); вызовы раз в 20 мс до смены ответа | `no_handler`, не позже 1 с после ответа `201` |
| 3 | `docker compose stop control-plane`; 20 вызовов; `GET /readyz` data plane | control plane не отвечает, все 20 — `no_handler`, `readyz` = 200 |
| 4 | `docker compose start control-plane`, ждать его `readyz`; `PUT` хука с новым обязательным полем `currency`; прежний payload раз в 100 мс не дольше 15 с | ответ стал `400` |
| 5* | путь T3–T5 через API: `POST /api/v1/tenants`, `POST .../console-sessions` (токен тенанта), `POST /api/v1/tenant/hooks/{hook}/modules` (multipart, `discount.wasm`), опрос модуля до `validated` не дольше 15 с, `PUT .../binding` (активация, конфиг `threshold=1000`, `percent=10`); вызовы с `currency` и `lifetime_spend: 1500` до исхода, отличного от `no_handler`/`unavailable` | `ok`, `discount_percent == 10`, `module_hash` совпадает с `content_hash` загруженного модуля |
| 6* | `validate-module /fixtures/http-call.wasm` | `ok: false`, проверка `imports` провалена |
| 7* | `validate-module /fixtures/discount.wasm` | `ok: true` |
| 8* | `PATCH /api/v1/tenants/{id}` `{"rate_limit_rps": 5}`; вызовы подряд (раз в 5 мс, не дольше 3 с) до первого не-`ok` | HTTP 429, `quota_exceeded`, не позже 1 с после ответа `PATCH` |
| 9* | `PATCH {"rate_limit_rps": 0}`; через 1 с после ответа 10 вызовов подряд | все 10 — `ok` |

\* — уровень «желательно», `--skip-should` их пропускает. Шаги 8–9 идут только если в шаге 5 модуль исполнился. Секунда ожидания в шаге 9 нужна, чтобы отличить снятый лимит: при живом лимите 5 за это время bucket снова полон (пять вызовов), и десять вызовов подряд не пройдут.

Колонка TIME — время всего шага; ключевые замеры (время распространения, длительность `stop`, время подхвата после отказа) — в колонке ACTUAL. Если шаг оставляет систему непригодной для следующих (нет ключа, хук не создан, control plane не поднялся), остальные шаги выводятся как FAIL с пометкой `not run`. Если прогон прерван, пока control plane остановлен, сценарий запускает его обратно.

## Повторяемость

Каждый прогон берёт суффикс `r<unix-секунды в base36>`: хук `checkout.discount_<suffix>`, тенант `merchant-a-<suffix>`, ключ `e2e-<suffix>`. Генератор нагрузки: хук `checkout.discount_<suffix>`, тенанты `merchant-a-<suffix>` и `merchant-b-<suffix>`, ключ `loadgen-<suffix>`. Сценарий проходит повторно на том же compose без `down -v` и не зависит от прошлых прогонов. Хуки, тенанты и ключи прошлых прогонов остаются в базе; удалить всё — `docker compose down -v`.

## Генератор нагрузки

```bash
cd e2e
go run ./cmd/loadgen              # таблица тенант × фаза, код выхода 1 при любом FAIL
```

Подготовка через API тем же путём T3–T5: ключ оператора (CLI), хук, merchant-a без лимита и merchant-b с `rate_limit_rps: 50`, у обоих `discount.wasm`; ждёт `ok` от data plane для обоих. Затем две фазы, каждая с прогревом 2 с (не учитывается) и замером 10 с:

- «тихо» — merchant-a, 50 вызовов/с;
- «шумно» — merchant-a, те же 50 вызовов/с, плюс merchant-b, 5000 вызовов/с.

Открытая модель: вызовы уходят по расписанию (каждый в своей горутине), ответ не задерживает следующий вызов. У каждого тенанта свой `http.Client` и свой `Transport` с числом соединений до 512, чтобы 5000 вызовов/с merchant-b не вставали в клиентскую очередь merchant-a; адрес data plane резолвится один раз (на Windows каждое разрешение имени держит поток ОС). Если data plane медленнее нагрузки, вызовы ждут соединение, и это ожидание входит в их латентность. Латентность считается от отправки запроса до прочитанного ответа; исход — заголовок `X-Hook-Outcome`, иначе `http_<код>`, при ошибке или таймауте клиента `error`.

Отчёт: на тенанта и фазу — число вызовов, p50, p99, max, счётчики исходов. Пороги (спек M2 §9.3), нарушение даёт строку `FAIL` и код выхода 1:

| Порог | Флаг | По умолчанию |
|---|---|---|
| merchant-a: 100% `ok` в обеих фазах | | |
| p99 merchant-a «шумно» ≤ p99 «тихо» + запас | `-max-p99-growth` | 10 мс |
| доля `quota_exceeded` у merchant-b | `-min-b-quota-share` | 0.9 |
| у merchant-b нет `timeout` | | |

Остальные флаги: `-phase` (10 с), `-warmup` (2 с), `-a-rps` (50), `-b-rps` (5000), `-b-quota` (50), `-call-timeout` (5 с), `-compose-dir`, `-fixtures`, `-control-plane`, `-data-plane`.

Перцентили на Windows ненадёжны (тик часов около 0.5 мс): локально смотрите на исходы, пороги по латентности имеют смысл на Linux.

## CI

Джоба `e2e` в `.github/workflows/ci.yml`: `docker compose up -d --build --wait control-plane dataplane`, `go test` с `E2E=1`, затем `go run ./cmd/loadgen`, при падении — `docker compose ps -a` и логи compose, в конце всегда `docker compose down -v`.

## Если упало

```bash
docker compose ps -a
docker compose logs dataplane control-plane
```

Data plane пишет `snapshot applied` с номером версии на каждый принятый снимок и `snapshot rejected or control plane unreachable` с `retry_in` на каждую неудачную попытку long-poll.
