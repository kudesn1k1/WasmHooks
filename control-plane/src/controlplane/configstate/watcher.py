import asyncio
import contextlib
import logging

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.tables import config_state

log = logging.getLogger(__name__)


class VersionWatcher:
    """Tracks the snapshot version and wakes long-poll waiters when it changes.

    One background task per process polls config_state; waiters block on an
    asyncio.Event that is set and replaced on every change, the Python
    counterpart of closing a channel and making a new one. Polling works with
    several replicas and with writes that bypass the API. LISTEN/NOTIFY can be
    added later as an accelerator behind the same interface (decision D21).
    """

    def __init__(self, engine: AsyncEngine, interval_s: float) -> None:
        self._engine = engine
        self._interval_s = interval_s
        self._version = 0
        self._changed = asyncio.Event()
        self._task: asyncio.Task[None] | None = None

    def current(self) -> int:
        return self._version

    async def start(self) -> None:
        # A database that is down at start-up must not keep the process from
        # starting: /readyz reports it, and the background poll catches up.
        try:
            await self.poll_once()
        except Exception:
            log.exception("config version read failed at start; polling continues")
        self._task = asyncio.create_task(self._run(), name="config-version-watcher")

    async def stop(self) -> None:
        task, self._task = self._task, None
        if task is None:
            return
        task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await task

    async def poll_once(self) -> None:
        version = await self._read()
        if version != self._version:
            self._version = version
            changed, self._changed = self._changed, asyncio.Event()
            changed.set()

    async def wait_newer(self, after: int, timeout_s: float) -> bool:
        """Waits until the version exceeds after; False if timeout_s passes first."""
        loop = asyncio.get_running_loop()
        deadline = loop.time() + timeout_s
        while self._version <= after:
            remaining = deadline - loop.time()
            if remaining <= 0:
                return False
            changed = self._changed
            try:
                await asyncio.wait_for(changed.wait(), remaining)
            except TimeoutError:
                return self._version > after
        return True

    async def _run(self) -> None:
        while True:
            await asyncio.sleep(self._interval_s)
            try:
                await self.poll_once()
            except Exception:
                log.exception("config version poll failed")

    async def _read(self) -> int:
        async with self._engine.connect() as conn:
            result = await conn.execute(
                select(config_state.c.version).where(config_state.c.id == 1)
            )
            return int(result.scalar_one())
