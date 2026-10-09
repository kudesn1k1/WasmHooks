import asyncio

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncConnection, AsyncEngine

from controlplane.configstate.schemas import (
    APIKeyOut,
    BindingOut,
    HookDefOut,
    Snapshot,
    TenantOut,
)
from controlplane.configstate.watcher import VersionWatcher
from controlplane.tables import api_keys, bindings, config_state, hooks, modules, tenants


async def read_snapshot(conn: AsyncConnection) -> Snapshot:
    """Reads the whole configuration. The caller owns the transaction; it must
    be one consistent read so the version matches the rows."""
    version = (
        await conn.execute(select(config_state.c.version).where(config_state.c.id == 1))
    ).scalar_one()

    key_rows = await conn.execute(
        select(api_keys.c.id, api_keys.c.name, api_keys.c.sha256)
        .where(api_keys.c.revoked_at.is_(None))
        .order_by(api_keys.c.id)
    )
    hook_rows = await conn.execute(
        select(
            hooks.c.name,
            hooks.c.def_version,
            hooks.c.input_schema,
            hooks.c.output_schema,
            hooks.c.timeout_ms,
            hooks.c.memory_max_pages,
            hooks.c.allowed_host_functions,
            hooks.c.allowed_effect_types,
            hooks.c.sample_input,
        ).order_by(hooks.c.name)
    )
    tenant_rows = await conn.execute(
        select(
            tenants.c.external_id, tenants.c.concurrency_limit, tenants.c.rate_limit_rps
        ).order_by(tenants.c.external_id)
    )
    binding_rows = await conn.execute(
        select(
            tenants.c.external_id.label("tenant_id"),
            hooks.c.name.label("hook"),
            modules.c.content_hash.label("module_hash"),
            bindings.c.config,
            bindings.c.config_version,
        )
        .select_from(bindings)
        .join(tenants, tenants.c.id == bindings.c.tenant_id)
        .join(hooks, hooks.c.id == bindings.c.hook_id)
        .join(modules, modules.c.id == bindings.c.active_module_id)
        .order_by(tenants.c.external_id, hooks.c.name)
    )

    return Snapshot(
        version=int(version),
        api_keys=[APIKeyOut.model_validate(r._mapping) for r in key_rows],
        hooks=[HookDefOut.model_validate(r._mapping) for r in hook_rows],
        tenants=[TenantOut.model_validate(r._mapping) for r in tenant_rows],
        bindings=[BindingOut.model_validate(r._mapping) for r in binding_rows],
    )


class SnapshotService:
    """Serves the serialized snapshot, building it at most once per version."""

    def __init__(self, engine: AsyncEngine, watcher: VersionWatcher) -> None:
        # One consistent read: the version and the rows it describes.
        self._engine = engine.execution_options(
            isolation_level="REPEATABLE READ", postgresql_readonly=True
        )
        self._watcher = watcher
        self._cached: tuple[int, bytes] | None = None
        self._lock = asyncio.Lock()

    async def read(self) -> Snapshot:
        """Reads the snapshot afresh, bypassing the cache, in one consistent
        transaction."""
        async with self._engine.begin() as conn:
            return await read_snapshot(conn)

    async def current(self) -> tuple[int, bytes]:
        # Bounded staleness by design: the watcher lags the database by at most
        # one poll interval, and so may this answer. Long-poll waiters are woken
        # by the same watcher, so they never see a version older than the one
        # that woke them. Do not turn this into a database read per request.
        cached = self._cached
        if cached is not None and cached[0] >= self._watcher.current():
            return cached
        async with self._lock:  # single-flight: concurrent misses build once
            cached = self._cached
            if cached is not None and cached[0] >= self._watcher.current():
                return cached
            snap = await self.read()
            self._cached = (snap.version, snap.model_dump_json().encode())
            return self._cached
