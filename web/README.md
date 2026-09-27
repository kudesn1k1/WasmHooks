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

## Стек

Next.js 15 (App Router), TypeScript (strict), Tailwind CSS 4 и shadcn/ui (Radix), TanStack Query, Vitest и React Testing Library. React Hook Form, Zod, Zustand, MSW, Recharts и Playwright подключаются вместе с первыми экранами, которым они нужны.

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
```

Слой импортирует только слои ниже себя. Слайс наружу отдаёт только то, что экспортирует его `index.ts`. Файлы в `src/app/**/page.tsx` тонкие: они только подключают страницу из `views`.

## shadcn/ui

Конфигурация лежит в `components.json`, компоненты — в `src/shared/ui`. Новый компонент добавляется командой `npx shadcn@latest add <имя>`. Реестр `ui.shadcn.com` доступен не из всех сетей: если команда падает по таймауту, нужен VPN.
