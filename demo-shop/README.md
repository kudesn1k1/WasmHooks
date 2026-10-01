# demo-shop

Бэкенд демо-магазина WasmHooks: оператор платформы с хуками расчёта скидки и валидации заказа, отдаёт API витрине.

- Владелец: backend, Python (см. `docs/TEAM.md`).
- Публикует: `api/demo-shop.openapi.yaml` (API для витрины demo-shop).
- Потребляет: `api/invoke.openapi.yaml` (вызывает хуки data plane через Python SDK, как оператор).
- Реализация начинается после M0, ведёт backend, Python.
