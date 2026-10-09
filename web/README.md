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

## Моки API

Пока control plane не готов, в `npm run dev` запросы к API перехватывает MSW (`src/mocks`): консоль тенанта `merchant-a` с хуками `checkout.discount` и `order.validate`. Состояние живёт в памяти вкладки и сбрасывается при перезагрузке.

- Загруженная версия около 3 с проверяется, потом становится `validated`. Если в имени файла есть `http`, она отклоняется с причиной из проверки `imports`.
- В лог вызовов каждые несколько секунд добавляются новые записи, так что видно обновление опросом.
- Настоящий control plane подключается через прокси Next.js (задача F2 в `docs/handoff/frontend.md`). Напрямую из браузера в control plane ходить нельзя: CORS там нет.

Тесты (Vitest) используют те же обработчики через `msw/node`.

### API консоли тенанта

Контракт — `api/control-plane.openapi.yaml`, он генерируется из кода control plane. Моки повторяют его формы, схемы ответов описаны в `src/entities/*/model/schema.ts`. Тенант определяется токеном консоли, поэтому его id в путях нет. Ошибки приходят в формате RFC 9457 (`application/problem+json`).

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
