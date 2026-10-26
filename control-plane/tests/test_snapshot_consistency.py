"""Decision D20, read side: a snapshot of version v contains exactly the
changes with version <= v, even when a writer commits while it is being read.

read_snapshot issues several queries (version, keys, hooks, tenants,
bindings). Under READ COMMITTED a commit landing between them would put a
newer hook into a snapshot labelled with an older version, and a binding
could reference a hook the snapshot does not have, which the data plane
rejects. The service reads in one REPEATABLE READ transaction; this test
commits a change exactly between the first and the second query.
"""

from typing import Any

import pytest
from sqlalchemy.ext.asyncio import AsyncConnection, AsyncEngine

from controlplane.configstate import snapshot as snapshot_module
from controlplane.configstate.snapshot import SnapshotService
from controlplane.configstate.version import bump_config_version
from controlplane.configstate.watcher import VersionWatcher
from tests.factories import add_hook


class CommitAfterFirstQuery:
    """Wraps the reader's connection: after its first statement, another
    connection inserts a hook and commits."""

    def __init__(self, conn: AsyncConnection, engine: AsyncEngine) -> None:
        self._conn = conn
        self._engine = engine
        self._calls = 0

    async def execute(self, *args: Any, **kwargs: Any) -> Any:
        result = await self._conn.execute(*args, **kwargs)
        self._calls += 1
        if self._calls == 1:
            async with self._engine.begin() as writer:
                await add_hook(writer, "late.hook")
                await bump_config_version(writer, "hook.created", {"name": "late.hook"})
        return result


async def test_snapshot_is_one_consistent_read(
    engine: AsyncEngine, monkeypatch: pytest.MonkeyPatch
) -> None:
    real = snapshot_module.read_snapshot

    async def interleaved(conn: AsyncConnection) -> snapshot_module.Snapshot:
        return await real(CommitAfterFirstQuery(conn, engine))  # type: ignore[arg-type]

    monkeypatch.setattr(snapshot_module, "read_snapshot", interleaved)
    service = SnapshotService(engine, VersionWatcher(engine, 0.05))

    snap = await service.read()

    # The read started before the late commit: version 1 and no late.hook.
    # Under READ COMMITTED it would see late.hook while reporting version 1.
    assert snap.version == 1
    assert [h.name for h in snap.hooks] == []

    monkeypatch.setattr(snapshot_module, "read_snapshot", real)
    after = await service.read()
    assert after.version == 2
    assert [h.name for h in after.hooks] == ["late.hook"]
