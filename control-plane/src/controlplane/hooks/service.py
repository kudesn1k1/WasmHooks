"""Operator hooks: the reference slice for every other control-plane feature.

The pattern: validate the input, open one transaction, read and lock what the
change depends on, write, and if the change is visible in the snapshot, call
bump_config_version once in the same transaction.
"""

from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.configstate.version import bump_config_version
from controlplane.errors import ProblemError
from controlplane.hooks import repo
from controlplane.hooks.rules import check_hook_spec, normalized
from controlplane.hooks.schemas import Hook, HookCreate, HookSpec

NOT_FOUND = "Хук не найден"


def _to_hook(row: repo.HookRow) -> Hook:
    return Hook(
        name=row.name,
        def_version=row.def_version,
        created_at=row.created_at,
        updated_at=row.updated_at,
        **row.spec.model_dump(),
    )


class HookService:
    def __init__(self, engine: AsyncEngine) -> None:
        self._engine = engine

    async def list_all(self) -> list[Hook]:
        async with self._engine.connect() as conn:
            return [_to_hook(row) for row in await repo.list_hooks(conn)]

    async def get(self, name: str) -> Hook:
        async with self._engine.connect() as conn:
            row = await repo.get_hook(conn, name)
        if row is None:
            raise ProblemError(404, NOT_FOUND, name)
        return _to_hook(row)

    async def create(self, data: HookCreate) -> Hook:
        spec = normalized(HookSpec(**data.model_dump(exclude={"name"})))
        check_hook_spec(spec)
        async with self._engine.begin() as conn:
            row = await repo.insert_hook(conn, data.name, spec)
            if row is None:
                raise ProblemError(409, "Хук с таким именем уже есть", data.name)
            await bump_config_version(conn, "hook.created", {"name": row.name, "def_version": 1})
        return _to_hook(row)

    async def replace(self, name: str, spec: HookSpec) -> Hook:
        spec = normalized(spec)
        check_hook_spec(spec)
        async with self._engine.begin() as conn:
            # The row lock serializes concurrent PUTs of one hook: each sees
            # the previous one's def_version, and no change is lost.
            current = await repo.get_hook(conn, name, for_update=True)
            if current is None:
                raise ProblemError(404, NOT_FOUND, name)
            # Allowed lists only grow this semester (main spec §10.2): a
            # running script may already import what would be taken away.
            narrowed = sorted(
                (set(current.spec.allowed_host_functions) - set(spec.allowed_host_functions))
                | (set(current.spec.allowed_effect_types) - set(spec.allowed_effect_types))
            )
            if narrowed:
                raise ProblemError(
                    409, "Нельзя сузить список разрешённых возможностей", ", ".join(narrowed)
                )
            if current.spec == spec:
                return _to_hook(current)
            updated = await repo.update_hook(conn, current.id, spec, current.def_version + 1)
            await bump_config_version(
                conn, "hook.updated", {"name": name, "def_version": updated.def_version}
            )
        return _to_hook(updated)
