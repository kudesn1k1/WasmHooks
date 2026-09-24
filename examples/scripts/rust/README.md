# Rust-скрипты: фикстуры и примеры

Cargo-workspace с восемью скриптами для платформы WasmHooks. Каждый крейт собирается в Extism-плагин (`wasm32-unknown-unknown`, `cdylib`) и экспортирует функцию `handle`. Собранные файлы лежат в `dataplane/testdata/wasm/` и используются контрактным набором песочницы, тестами executor-а, бенчмарками спайка и демо. `discount` заодно служит примером настоящего скрипта.

ABI скрипта не зависит от языка: платформе нужен wasm-модуль с экспортом `handle`, который читает вход и пишет выход через функции хоста Extism (`extism:host/env`). Rust здесь только язык фикстур, в Go-коде data plane нет ничего, что знает про Rust.

## Поведение

Тесты опираются на эту таблицу, менять поведение фикстур можно только вместе с тестами.

| Файл | Вход | Выход / поведение |
|---|---|---|
| `discount.wasm` | `{"cart_total": number, "customer": {"id": string, "lifetime_spend": number}}` | Конфиг `threshold` (число строкой, по умолчанию `"1000"`), `percent` (по умолчанию `"10"`). `{"result": {"discount_percent": P, "reason": "loyal_customer"}, "effects": []}` если `lifetime_spend >= threshold`, иначе `{"result": {"discount_percent": 0, "reason": "none"}, "effects": []}`. Логирует `info!("discount computed: {}", p)`. |
| `infinite-loop.wasm` | любой | Бесконечный цикл, который компилятор не может удалить (`std::hint::black_box`). |
| `memory-bomb.wasm` | любой | Растит `Vec<Vec<u8>>` блоками по 1 МиБ с `black_box`, пока аллокатор не упадёт. |
| `counter.wasm` | любой | Статический `AtomicU64`, инкремент, выход `{"result": {"count": N}, "effects": []}`. |
| `guest-error.wasm` | любой | Возвращает ошибку PDK с текстом `"guest failure: boom"`. |
| `echo-config.wasm` | `{"keys": [string]}` | `{"result": {"config": {"k": "v" или null}}, "effects": []}` — значение каждого запрошенного ключа из конфига. |
| `bad-output.wasm` | любой | `{"result": {"discount_percent": "not-a-number"}, "effects": [{"type": "forbidden.effect", "payload": {}}]}` |
| `http-call.wasm` | любой | Выполняет `http::request` на `https://example.com`, возвращает статус. |

Выход всех фикстур — компактный JSON без пробелов: тесты ищут подстроки вида `"k":"a"` и `"count":1`. Ошибка конфига или входа в `discount` и `echo-config` возвращается как ошибка PDK.

## Пересборка

Нужны Rust stable и target `wasm32-unknown-unknown` (`rustup target add wasm32-unknown-unknown`).

```bash
cd dataplane
go run ./tools/buildscripts
```

Инструмент запускает `cargo build --release --target wasm32-unknown-unknown` в этом каталоге, копирует `target/wasm32-unknown-unknown/release/<имя_с_подчёркиваниями>.wasm` в `dataplane/testdata/wasm/<имя-с-дефисами>.wasm` и печатает размер и sha256 каждого файла. Список крейтов берётся из `members` в `Cargo.toml`.

Проверить импорты и экспорты:

```bash
cd dataplane
go run ./tools/wasmimports testdata/wasm/*.wasm
```

У каждого файла должен быть экспорт `handle` и импорты только из `extism:host/env`.

Сборка не воспроизводима побайтово между машинами и версиями Rust: в бинарник попадают пути к исходникам зависимостей. Источник истины — закоммиченные `.wasm`. `Cargo.lock` закоммичен, чтобы версии зависимостей не менялись сами. После пересборки хеши меняются, поэтому всё, что ссылается на `module_hash` фикстур (например, снимок демо), нужно обновить в том же коммите.
