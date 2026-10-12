import asyncio
import logging
import time
from collections.abc import AsyncIterator

import pytest
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.configstate.version import bump_config_version
from controlplane.configstate.watcher import VersionWatcher


@pytest.fixture
async def watcher(engine: AsyncEngine) -> AsyncIterator[VersionWatcher]:
    w = VersionWatcher(engine, interval_s=0.05)
    await w.start()
    yield w
    await w.stop()


async def bump(engine: AsyncEngine) -> int:
    async with engine.begin() as conn:
        return await bump_config_version(conn, "test", {})


async def test_starts_at_database_version(engine: AsyncEngine) -> None:
    await bump(engine)
    w = VersionWatcher(engine, interval_s=0.05)
    await w.start()
    try:
        assert w.current() == 2
    finally:
        await w.stop()


async def test_wait_times_out_without_changes(watcher: VersionWatcher) -> None:
    start = time.monotonic()
    assert await watcher.wait_newer(watcher.current(), 0.2) is False
    assert time.monotonic() - start >= 0.2


async def test_wait_wakes_on_change(engine: AsyncEngine, watcher: VersionWatcher) -> None:
    waiter = asyncio.create_task(watcher.wait_newer(watcher.current(), 5))
    await asyncio.sleep(0.05)
    changed_at = time.monotonic()
    await bump(engine)
    assert await waiter is True
    assert time.monotonic() - changed_at < 0.5


async def test_wait_returns_at_once_when_already_newer(watcher: VersionWatcher) -> None:
    start = time.monotonic()
    assert await watcher.wait_newer(watcher.current() - 1, 5) is True
    assert time.monotonic() - start < 0.05


async def test_canceled_waiter_leaves_watcher_working(
    engine: AsyncEngine, watcher: VersionWatcher, caplog: pytest.LogCaptureFixture
) -> None:
    # A data plane that restarts mid long-poll abandons its request.
    caplog.set_level(logging.ERROR)
    waiter = asyncio.create_task(watcher.wait_newer(watcher.current(), 30))
    await asyncio.sleep(0.1)
    waiter.cancel()
    with pytest.raises(asyncio.CancelledError):
        await waiter

    next_waiter = asyncio.create_task(watcher.wait_newer(watcher.current(), 5))
    await bump(engine)
    assert await next_waiter is True
    assert not [r for r in caplog.records if r.levelno >= logging.ERROR]


async def test_stop_is_idempotent(engine: AsyncEngine) -> None:
    w = VersionWatcher(engine, interval_s=0.05)
    await w.start()
    await w.stop()
    await w.stop()
