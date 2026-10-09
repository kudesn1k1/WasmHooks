# web

Фронтенд WasmHooks: консоль оператора, консоль тенанта и витрина демо-магазина (Next.js, route groups `(operator)`, `(tenant)`, `(storefront)`).

- Владелец: frontend (см. `docs/TEAM.md`).
- Потребляет: `api/control-plane.openapi.yaml` (консоли), `api/demo-shop.openapi.yaml` (витрина). В data plane не ходит никогда.
- Моки API (MSW) по контрактам из `api/` для работы до готовности бэкенда.
- Задачи Milestone 1 и точки синхронизации с backend: `docs/handoff/frontend.md`.

## Запуск

Нужен Node.js 22 или новее.

```bash
cd web
npm install
npm run dev        # http://localhost:3000
```

| Адрес | Что это |
|---|---|
| `/`, `/cart` | витрина демо-магазина |
| `/tenant/hooks`, `/tenant/invocations` | консоль тенанта |
| `/operator/hooks`, `/operator/tenants` | консоль оператора (MVP) |

Проверки, те же, что в CI:

```bash
npm run typecheck
npm run lint
npm test
npm run build
```

## Моки API и прокси

Браузер ходит только на свой origin, в `/api/v1/*`. Next.js проксирует эти запросы в control plane: rewrite в `next.config.ts` ведёт на `${CONTROL_PLANE_URL}/api/v1/*`. По умолчанию `CONTROL_PLANE_URL` равен `http://localhost:8000`. CORS поэтому не нужен. Rewrites читаются при сборке, так что для `next build` переменная нужна уже на этапе сборки.

Перед прокси запросы может перехватить MSW (`src/mocks`). Режим задаётся в `.env.local` переменной `NEXT_PUBLIC_API_MOCKING`:

| Режим | Что делает |
|---|---|
| `all` (по умолчанию в `npm run dev`) | отвечают все моки, бэкенд не нужен |
| `partial` | моки отвечают только за эндпоинты, которых ещё нет в API; остальное уходит через прокси в control plane |
| `disabled` | моков нет, всё идёт в control plane |

В продакшен-сборке моки выключены всегда.

Что ещё не реализовано на бэкенде, перечислено в одном месте: `src/mocks/pending.ts`, группа обработчиков и номер задачи backend. Когда задача смержена, её группа убирается из списка отдельным PR. Группа `invocations` остаётся до MVP.

Пример `.env.local` для работы против control plane из `docker compose`:

```
NEXT_PUBLIC_API_MOCKING=partial
CONTROL_PLANE_URL=http://localhost:8000
```

Моки ведут себя как настоящий API:

- без заголовка `Authorization` отвечают `401` «Не авторизован» с `WWW-Authenticate: Bearer`; в режиме `all` подходит любой непустой токен;
- данные: консоль тенанта `merchant-a` с хуками `checkout.discount` и `order.validate`; состояние живёт в памяти вкладки;
- загруженная версия около 3 с проверяется, потом становится `validated`; если в имени файла есть `http`, она отклоняется с причиной из проверки `imports`;
- в лог вызовов каждые несколько секунд добавляются новые записи.

Тесты (Vitest) используют все обработчики через `msw/node`.

### Токены консолей

`apiRequest` сам подставляет `Authorization: Bearer <токен>`: на `/api/v1/tenant/*` идёт токен тенанта, на остальные пути — ключ установки. Оба токена хранятся только в `sessionStorage`. Ответ `401` стирает токен той консоли, которой он пришёл, и консоль снова показывает экран входа. Ответ `501` показывается как «Ещё не реализовано на бэкенде: T…».

### API консоли тенанта

Контракт — `api/control-plane.openapi.yaml`, он генерируется из кода control plane. Моки повторяют его формы, схемы ответов описаны в `src/entities/*/model/schema.ts`. Поля, которые в контракте `nullable` или необязательны, схемы принимают и как `null`, и как отсутствующие (`.nullish()`). Тенант определяется токеном консоли, поэтому его id в путях нет. Ошибки приходят в формате RFC 9457 (`application/problem+json`).

Лог вызовов `GET /api/v1/tenant/invocations?outcome=&hook=&limit=` (`{ items: Invocation[] }`, новые сверху) в контракт не входит до MVP и есть только в моках.

## Стек

Next.js 15 (App Router), TypeScript (strict), Tailwind CSS 4 и shadcn/ui (Radix), TanStack Query, Zod (проверка ответов API), MSW, Vitest и React Testing Library. React Hook Form, Zustand, Recharts и Playwright подключаются вместе с первыми экранами, которым они нужны.

## Структура

Feature-Sliced Design, адаптированный под App Router:

```
src/
  app/          роутинг Next.js и слой app из FSD: layout-ы, провайдеры (_providers)
    (storefront)/  (tenant)/  (operator)/   route groups со своими layout-ами
  views/        страницы (слой pages из FSD; имя pages в Next.js зарезервировано)
  widgets/      крупные блоки: оболочка консоли, шапка витрины
  features/     действия пользователя: загрузка модуля, активация версии
  entities/     сущности предметной области: хук, модуль, тенант, вызов
  shared/       ui (shadcn), lib, config, api
mocks/          MSW-обработчики и данные; вне слоёв FSD, только для dev и тестов
```

Слой импортирует только слои ниже себя. Слайс наружу отдаёт только то, что экспортирует его `index.ts`. Файлы в `src/app/**/page.tsx` тонкие: они только подключают страницу из `views`.

## shadcn/ui

Конфигурация лежит в `components.json`, компоненты — в `src/shared/ui`. Новый компонент добавляется командой `npx shadcn@latest add <имя>`. Реестр `ui.shadcn.com` доступен не из всех сетей: если команда падает по таймауту, нужен VPN.
