# control-plane

Control plane WasmHooks: API-ключи, тенанты, хуки, модули, привязки, квоты. Решает, что разрешено; в пути вызова не участвует.

- Владелец: backend, Python (см. `docs/TEAM.md`).
- Реализует: `api/dataplane-internal.openapi.yaml` (снимок конфигурации для data plane).
- Потребляет: `POST /internal/v1/modules/validate` из того же контракта.
- Публикует: `api/control-plane.openapi.yaml` (API консолей, генерируется из FastAPI).
- Реализация ведётся backend, Python, начиная с M0.
