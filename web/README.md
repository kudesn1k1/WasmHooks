# web

Фронтенд WasmHooks: консоль оператора, консоль тенанта и витрина демо-магазина (Next.js, route groups `(operator)`, `(tenant)`, `(storefront)`).

- Владелец: frontend (см. `docs/TEAM.md`).
- Потребляет: `api/control-plane.openapi.yaml` (консоли), `api/demo-shop.openapi.yaml` (витрина). В data plane не ходит никогда.
- Моки API (MSW) по контрактам из `api/` для работы до готовности бэкенда.
- Реализация ведётся frontend, начиная с M0.
