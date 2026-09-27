# web

Фронтенд WasmHooks: консоль оператора, консоль тенанта и витрина демо-магазина (Next.js, route groups `(operator)`, `(tenant)`, `(storefront)`).

- Владелец: frontend (см. `docs/TEAM.md`).
- Потребляет: `api/control-plane.openapi.yaml` (консоли), `api/demo-shop.openapi.yaml` (витрина). В data plane не ходит никогда.
- Моки API (MSW) по контрактам из `api/` для работы до готовности бэкенда.

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
- Чтобы работать с настоящим бэкендом, задай в `.env.local` `NEXT_PUBLIC_API_MOCKING=disabled` и `NEXT_PUBLIC_API_BASE_URL=<адрес control plane>/api/v1`.

Тесты (Vitest) используют те же обработчики через `msw/node`.

### Черновик API консоли тенанта

Контракта `api/control-plane.openapi.yaml` ещё нет. Фронтенд пока работает по этому черновику, составленному по модели данных основного спека (§10.1). Схемы ответов описаны в `src/entities/*/model/schema.ts`. Тенант определяется сессией, поэтому его id в путях нет. Ошибки приходят в формате RFC 9457 (`application/problem+json`).

| Метод и путь | Ответ | Назначение |
|---|---|---|
| `GET /api/v1/tenant/hooks` | `{ items: TenantHook[] }` | хуки с привязкой тенанта |
| `GET /api/v1/tenant/hooks/{hook}` | `TenantHook` | один хук |
| `GET /api/v1/tenant/hooks/{hook}/modules` | `{ items: Module[] }` | версии модуля, новые сверху |
| `POST /api/v1/tenant/hooks/{hook}/modules` | `201 Module` | загрузка `.wasm` (multipart, поле `file`); 409, если такой хеш уже есть |
| `PUT /api/v1/tenant/hooks/{hook}/binding` | `Binding` | тело `{ active_module_id }`; активация и откат; 409, если версия не `validated` |
| `GET /api/v1/tenant/invocations?outcome=&hook=&limit=` | `{ items: Invocation[] }` | лог вызовов, новые сверху |

Когда контракт появится, схемы сверяются с ним или заменяются сгенерированными, а моки переписываются по нему.

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
