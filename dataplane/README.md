# Data plane WasmHooks

Data plane Milestone 0: gateway и executor в одном процессе. Обслуживает
публичный Invoke API (`api/invoke.openapi.yaml`), загружает снимок
конфигурации в формате `api/dataplane-internal.openapi.yaml` и исполняет
скрипт тенанта — WebAssembly-модуль с ABI Extism — в песочнице с лимитом
времени и лимитом памяти. Сломанный скрипт одного тенанта не должен
замедлять или ломать вызовы другого. Полное описание архитектуры —
`docs/superpowers/specs/2026-09-24-milestone-0-design.md`; этот README
покрывает только сборку, запуск и демонстрацию того, что уже реализовано.

## Сборка и тесты

```bash
go build ./...
go test ./...
```

Go 1.26, без cgo, собирается и тестируется на `windows/amd64` и
`linux/amd64`.

### Запуск бинарника вручную

`cmd/dataplane` принимает файл снимка конфигурации и директорию со
скомпилированными `.wasm`-модулями:

```
go run ./cmd/dataplane -listen :8080 -snapshot <snapshot.json> -modules-dir <dir>
```

Флаги: `-listen` (по умолчанию `:8080`), `-snapshot` (обязателен),
`-modules-dir` (обязателен), `-log-level` (`debug|info|warn|error`, по
умолчанию `info`), `-executor-id`, `-pool-global-max`,
`-pool-acquire-timeout`, `-pool-max-uses`, `-pool-idle-ttl`. Как только
слушатель забиндован, процесс печатает ровно одну строку `listening on
<host:port>` в stdout; всё остальное — структурированный JSON в stderr.
`GET /readyz` отдаёт 200, как только снимок загружен и его привязанные
модули скомпилированы; `GET /healthz` отдаёт 200 сразу после старта
процесса.

**Директория модулей адресуется по содержимому, а не по имени.** Каждый
файл в `-modules-dir` должен называться `<sha256-hex>.wasm` — строчный hex
SHA-256 от байтов модуля, без префикса `sha256:` (см. `internal/modstore`).
Закоммиченные фикстуры в `testdata/wasm/` названы по крейту
(`discount.wasm`, `infinite-loop.wasm`, ...) для читаемости, поэтому
`-modules-dir` нельзя направить прямо на `testdata/wasm`. Сначала нужно
собрать копию с именами-хешами:

```bash
# из dataplane/
mkdir -p /tmp/wasmhooks-modules
for f in testdata/wasm/*.wasm; do
  hash=$(sha256sum "$f" | cut -d' ' -f1)
  cp "$f" "/tmp/wasmhooks-modules/$hash.wasm"
done
go run ./cmd/dataplane -listen 127.0.0.1:8080 \
  -snapshot ../examples/demo/snapshot.json \
  -modules-dir /tmp/wasmhooks-modules
```

```powershell
# из dataplane/
New-Item -ItemType Directory -Force modules-tmp | Out-Null
Get-ChildItem testdata/wasm/*.wasm | ForEach-Object {
    $hash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower()
    Copy-Item $_.FullName "modules-tmp/$hash.wasm"
}
go run ./cmd/dataplane -listen 127.0.0.1:8080 `
  -snapshot ../examples/demo/snapshot.json `
  -modules-dir modules-tmp
```

`go run ./cmd/demo` (ниже) делает это же автоматически во временной
директории и дополнительно проверяет, что хеши совпадают с тем, что ожидает
`examples/demo/snapshot.json`, — это самый простой способ поднять data
plane с демо-конфигом.

## Демо одной командой

```bash
cd dataplane
go run ./cmd/demo
```

Демо собирает `./cmd/dataplane` во временную директорию, готовит директорию
модулей с именами-хешами из `testdata/wasm`, запускает бинарник против
`examples/demo/snapshot.json`, ждёт `/readyz`, прогоняет сценарий из раздела
2 и раздела 14 спека M0 по HTTP, печатает таблицу (шаг, тенант, ожидаемый
исход, фактический исход, HTTP-статус, латентность, PASS/FAIL) и корректно
останавливает дочерний процесс (`Process.Kill` на Windows; `SIGTERM`, затем
`Kill` через 5 с на остальных платформах). Код возврата ненулевой, если хоть
один шаг FAIL, поэтому демо заодно служит smoke-тестом.

Флаги:
- `-target <url>`: прогнать сценарий против уже запущенного data plane,
  вместо того чтобы собирать и стартовать свой (например, против инстанса,
  поднятого вручную командой выше).
- `-api-key <key>`: bearer-токен для вызовов (по умолчанию — демо-ключ ниже).

Девять шагов сценария:

1. `merchant-a`, лояльный клиент (`lifetime_spend` 1500 ≥ порога 1000) →
   `ok`, `discount_percent` 10.
2. `merchant-a`, новый клиент (`lifetime_spend` 10) → `ok`,
   `discount_percent` 0.
3. `merchant-b` (привязан к фикстуре `infinite-loop`) → `timeout`, в
   пределах лимита хука (50 мс) плюс щедрый запас на планировщик.
4. Базовый уровень: 20 последовательных вызовов `merchant-a`, фиксируются
   p50 и max латентности.
5. `merchant-b` продолжает висеть в фоне (его пул насыщен зависшими
   вызовами), пока идут ещё 20 вызовов `merchant-a`: все по-прежнему `ok`;
   шаг падает только если max-латентность под нагрузкой превышает базовый
   max больше чем на 50 мс — доказательство, что зависший скрипт одного
   тенанта не замедляет другого.
6. `merchant-c` (привязан к фикстуре `memory-bomb`, `memory_max_pages: 64` =
   4 МиБ) → `handler_error` (аллокатор падает заметно раньше 50-мс лимита
   хука).
7. `merchant-z`, не зарегистрирован в снимке → `no_handler`.
8. `merchant-a`, payload без обязательного поля `customer` → HTTP 400.
9. `merchant-a` с неверным API-ключом → HTTP 401.

Пример успешного прогона:

```
#  TENANT                              EXPECTED                               GOT                            HTTP  LATENCY  RESULT
1  merchant-a                          ok, discount_percent=10                ok, discount_percent=10        200   6.2ms    PASS
2  merchant-a                          ok, discount_percent=0                 ok, discount_percent=0         200   1.1ms    PASS
3  merchant-b                          timeout, <=200ms                       timeout                        200   50.2ms   PASS
4  merchant-a (x20)                    20/20 ok                               20/20 ok, p50=0.5ms max=2.3ms  200   2.3ms    PASS
5  merchant-a (x20) + merchant-b hung  20/20 ok, max<=baseline+50ms (52.3ms)  20/20 ok, p50=1.1ms max=1.7ms  200   1.7ms    PASS
6  merchant-c                          handler_error                          handler_error                  200   4.1ms    PASS
7  merchant-z                          no_handler                             no_handler                     200   0.6ms    PASS
8  merchant-a                          HTTP 400                               HTTP 400                       400   0.5ms    PASS
9  merchant-a                          HTTP 401                               HTTP 401                       401   0.0ms    PASS

9/9 PASS
```

Если какой-то шаг падает, демо дополнительно печатает захваченный stderr-лог
data plane (структурированный JSON, `slog`), чтобы диагностировать причину
без повторного прогона.

Про Windows: монотонные часы там квантуются по тикам, поэтому латентность
меньше миллисекунды может отобразиться как `0.0ms`. Латентность всегда
печатается в миллисекундах с одним знаком после запятой; не стоит
интерпретировать `0.0ms` как что-то более точное, чем «очень быстро».

## Те же шаги через curl / PowerShell

Сначала запустите data plane вручную (см. выше), затем обращайтесь к нему
через `curl` или `Invoke-RestMethod`. Демо-ключ API —
`whk_demo_0123456789` (`examples/demo/snapshot.json` хранит его SHA-256).

### bash

```bash
BASE=http://127.0.0.1:8080
KEY=whk_demo_0123456789

# 1: ok, discount_percent 10
curl -s -w '\nHTTP %{http_code}\n' -X POST "$BASE/v1/hooks/checkout.discount/invoke" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"tenant_id":"merchant-a","payload":{"cart_total":9999,"customer":{"id":"c1","lifetime_spend":1500}}}'

# 2: ok, discount_percent 0
curl -s -w '\nHTTP %{http_code}\n' -X POST "$BASE/v1/hooks/checkout.discount/invoke" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"tenant_id":"merchant-a","payload":{"cart_total":10,"customer":{"id":"c2","lifetime_spend":10}}}'

# 3: timeout (фикстура infinite-loop)
curl -s -w '\nHTTP %{http_code}\n' -X POST "$BASE/v1/hooks/checkout.discount/invoke" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"tenant_id":"merchant-b","payload":{"cart_total":9999,"customer":{"id":"c1","lifetime_spend":1500}}}'

# 6: handler_error (фикстура memory-bomb)
curl -s -w '\nHTTP %{http_code}\n' -X POST "$BASE/v1/hooks/checkout.discount/invoke" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"tenant_id":"merchant-c","payload":{"cart_total":9999,"customer":{"id":"c1","lifetime_spend":1500}}}'

# 7: no_handler (незарегистрированный тенант)
curl -s -w '\nHTTP %{http_code}\n' -X POST "$BASE/v1/hooks/checkout.discount/invoke" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"tenant_id":"merchant-z","payload":{"cart_total":9999,"customer":{"id":"c1","lifetime_spend":1500}}}'

# 8: HTTP 400, payload не проходит input_schema (нет customer)
curl -s -w '\nHTTP %{http_code}\n' -X POST "$BASE/v1/hooks/checkout.discount/invoke" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"tenant_id":"merchant-a","payload":{"cart_total":10}}'

# 9: HTTP 401, неверный ключ
curl -s -w '\nHTTP %{http_code}\n' -X POST "$BASE/v1/hooks/checkout.discount/invoke" \
  -H "Authorization: Bearer wrong-key" -H 'Content-Type: application/json' \
  -d '{"tenant_id":"merchant-a","payload":{"cart_total":9999,"customer":{"id":"c1","lifetime_spend":1500}}}'
```

### PowerShell

```powershell
$Base = 'http://127.0.0.1:8080'
$Key  = 'whk_demo_0123456789'

# 1: ok, discount_percent 10
Invoke-RestMethod -Method Post -Uri "$Base/v1/hooks/checkout.discount/invoke" `
  -Headers @{ Authorization = "Bearer $Key" } -ContentType 'application/json' `
  -Body '{"tenant_id":"merchant-a","payload":{"cart_total":9999,"customer":{"id":"c1","lifetime_spend":1500}}}'

# 3: timeout (фикстура infinite-loop)
Invoke-RestMethod -Method Post -Uri "$Base/v1/hooks/checkout.discount/invoke" `
  -Headers @{ Authorization = "Bearer $Key" } -ContentType 'application/json' `
  -Body '{"tenant_id":"merchant-b","payload":{"cart_total":9999,"customer":{"id":"c1","lifetime_spend":1500}}}'

# 8: HTTP 400 — нужен PowerShell 7+ для -SkipHttpErrorCheck/-StatusCodeVariable
$resp = Invoke-RestMethod -Method Post -Uri "$Base/v1/hooks/checkout.discount/invoke" `
  -Headers @{ Authorization = "Bearer $Key" } -ContentType 'application/json' `
  -Body '{"tenant_id":"merchant-a","payload":{"cart_total":10}}' `
  -SkipHttpErrorCheck -StatusCodeVariable statusCode
"HTTP $statusCode"; $resp

# 9: HTTP 401
$resp = Invoke-RestMethod -Method Post -Uri "$Base/v1/hooks/checkout.discount/invoke" `
  -Headers @{ Authorization = 'Bearer wrong-key' } -ContentType 'application/json' `
  -Body '{"tenant_id":"merchant-a","payload":{"cart_total":9999,"customer":{"id":"c1","lifetime_spend":1500}}}' `
  -SkipHttpErrorCheck -StatusCodeVariable statusCode
"HTTP $statusCode"; $resp
```

Шаги 4 и 5 (базовая латентность и «a не замедляется зависшим b») сравнивают
20 вызовов между собой и не имеют смысла как разовые curl-команды —
для них нужен `go run ./cmd/demo`.

## Исходы и HTTP-статусы

Любой исход, кроме `quota_exceeded` и `unavailable`, — это HTTP 200:
статус-код описывает, сделала ли *платформа* свою работу, а не успех
скрипта тенанта (обоснование и прецедент — AWS Lambda `Invoke` — в разделе
6.1.3 спека M0). Полный контракт: `api/invoke.openapi.yaml`.

| outcome | HTTP | скрипт запускался? | смысл |
|---|---|---|---|
| `ok` | 200 | да | скрипт вернул валидный результат |
| `no_handler` | 200 | нет | хук существует, но у тенанта нет активного скрипта (или тенант не зарегистрирован) |
| `timeout` | 200 | да | скрипт превысил `timeout_ms` хука |
| `handler_error` | 200 | да | трап, превышение памяти, ошибка гостя, либо выход не прошёл `output_schema` |
| `quota_exceeded` | 429 | нет | превышена квота тенанта (только контракт; в M0 не реализовано) |
| `unavailable` | 503 | нет | пул насыщен, дедлайн истёк до запуска, модуль недоступен, внутренняя ошибка |
| — | 400 | нет | тело запроса невалидно, либо payload не проходит `input_schema` |
| — | 401 | нет | отсутствует или неверен API-ключ |
| — | 404 | нет | неизвестный хук |
| — | 413 | нет | тело запроса больше лимита в 1 МиБ |

В ответе всегда есть заголовок `X-Hook-Outcome`, дублирующий `outcome` из
тела; ошибки запроса (400/401/404/413) отдаются в формате RFC 9457
(`problem+json`) вместо обычного JSON.

## Структура пакетов и правило зависимостей

```
internal/
  sandbox/, sandbox/extismrt/, sandbox/sandboxtest/  абстракция рантайма, реализация на Extism, контрактные тесты
  pool/            пул инстансов на ключ (tenant, hook, def_version, module_hash, config_version)
  modstore/        байты модуля по хешу содержимого
  schema/          компиляция и проверка JSON Schema
  config/          модель снимка, FileSource, атомарный Store
  execproto/       контракт gateway <-> executor (только типы и интерфейс Executor)
  executor/        реализация execproto.Executor: кеш скомпилированных модулей, пулы, вызов, проверка выхода
  gateway/, gateway/httpapi/  допуск, маршрутизация, маппинг исходов в HTTP-статусы
  observe/         запись о вызове (в M0 — slog-приёмник)
  archtest/        правило зависимостей ниже, проверяется в CI через `go list -deps -json`
cmd/dataplane/     сборка зависимостей, HTTP-сервер, graceful shutdown
cmd/demo/          это демо
```

Правило (спек M0 §5.2, кратко): `gateway` может зависеть от `execproto`,
`config`, `schema`, `observe` и **никогда** — от `executor`, `pool`,
`sandbox`, `modstore`; `executor` может зависеть от всего перечисленного
плюс `sandbox`, `pool`, `modstore`, и **никогда** — от `gateway`.
`execproto` не зависит ни от чего внутреннего и содержит только простые
типы (повторяет будущий protobuf: примитивы, срезы, map, без указателей, без
`context`). Только `cmd/dataplane` импортирует и `gateway`, и `executor`, и
связывает их вместе. Именно это делает разделение gateway и executor-а на
два процесса в MVP механическим шагом (`execproto/grpc` плюс два
`main`-пакета), а не переписыванием; `internal/archtest` — fitness function,
которая держит это правило соблюдаемым.

## Пересборка wasm-фикстур

Rust-крейты фикстур лежат в `examples/scripts/rust/` (cargo workspace,
`extism-pdk`, таргет `wasm32-unknown-unknown`). Пересобрать закоммиченные
`.wasm`-файлы:

```bash
cd dataplane
go run ./tools/buildscripts
```

После `cargo build --release` инструмент печатает имя, размер и
`sha256:<hex>` каждой фикстуры. Сборки Rust не побайтово воспроизводимы
между машинами, поэтому пересборка обычно меняет хеши. Когда это
происходит, нужно обновить поля `module_hash` в
`examples/demo/snapshot.json` — `go run ./cmd/demo` проверяет это при
старте и, если забыть обновить, падает с понятным сообщением (называет
конкретную привязку и хеш), а не с непонятной ошибкой во время исполнения.
