"""Single-worker background orchestration of data-plane module validation."""

import asyncio
import logging
import random
import uuid
from typing import Protocol

from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.configstate.schemas import HookDefOut
from controlplane.dpclient.client import DataPlaneError, DataPlaneUnavailable
from controlplane.dpclient.schemas import ValidationReport
from controlplane.hooks import repo as hooks_repo
from controlplane.modules import repo
from controlplane.modules.schemas import ModuleStatus

logger = logging.getLogger(__name__)


class ValidationClient(Protocol):
    async def validate(
        self, module_hash: str, hook: HookDefOut, config: dict[str, str] | None = None
    ) -> ValidationReport: ...

    async def aclose(self) -> None: ...


class ModuleValidator:
    def __init__(
        self,
        engine: AsyncEngine,
        client: ValidationClient,
        *,
        backoff_base_s: float = 0.5,
        backoff_max_s: float = 30.0,
    ) -> None:
        self._engine = engine
        self._client = client
        self._backoff_base_s = backoff_base_s
        self._backoff_max_s = backoff_max_s
        self._queue: asyncio.Queue[uuid.UUID] = asyncio.Queue()
        self._task: asyncio.Task[None] | None = None

    async def start(self) -> None:
        if self._task is not None:
            return
        self._task = asyncio.create_task(self._run(), name="module-validator")

    async def stop(self) -> None:
        task = self._task
        self._task = None
        if task is not None:
            task.cancel()
            try:
                await task
            except asyncio.CancelledError:
                pass
        await self._client.aclose()

    def enqueue(self, module_id: uuid.UUID) -> None:
        self._queue.put_nowait(module_id)

    async def _run(self) -> None:
        await self._recover_pending()
        while True:
            module_id = await self._queue.get()
            try:
                await self._validate_until_answer(module_id)
            except asyncio.CancelledError:
                raise
            except Exception:
                logger.exception("module validation crashed; requeueing module %s", module_id)
                await asyncio.sleep(self._jitter(0))
                self.enqueue(module_id)
            finally:
                self._queue.task_done()

    async def _recover_pending(self) -> None:
        attempt = 0
        while True:
            try:
                async with self._engine.connect() as conn:
                    pending = await repo.list_pending_module_ids(conn)
            except asyncio.CancelledError:
                raise
            except Exception:
                delay = self._jitter(attempt)
                logger.exception(
                    "cannot recover pending module validations; retrying in %.3fs", delay
                )
                attempt += 1
                await asyncio.sleep(delay)
            else:
                for module_id in pending:
                    self.enqueue(module_id)
                return

    async def _validate_until_answer(self, module_id: uuid.UUID) -> None:
        async with self._engine.begin() as conn:
            target = await repo.get_validation_target(conn, module_id, for_update=True)
            if target is None or target.status not in ("uploaded", "validating"):
                return
            if target.status == "uploaded":
                await repo.mark_validating(conn, module_id)

        attempt = 0
        while True:
            hook, config = await self._context(target)
            try:
                report = await self._client.validate(target.content_hash, hook, config)
            except DataPlaneUnavailable as exc:
                delay = self._jitter(attempt)
                logger.warning(
                    "data plane unavailable validating module %s; retrying in %.3fs: %s",
                    module_id,
                    delay,
                    exc,
                )
            except DataPlaneError as exc:
                delay = self._jitter(attempt)
                logger.error(
                    "data plane rejected validation request for module %s; retrying in %.3fs: %s",
                    module_id,
                    delay,
                    exc,
                )
            else:
                status: ModuleStatus = "validated" if report.ok else "rejected"
                async with self._engine.begin() as conn:
                    await repo.finish_validation(conn, module_id, status, report)
                return
            attempt += 1
            await asyncio.sleep(delay)

    async def _context(
        self, target: repo.ValidationTarget
    ) -> tuple[HookDefOut, dict[str, str] | None]:
        async with self._engine.connect() as conn:
            hook_row = await hooks_repo.get_hook(conn, target.hook)
            if hook_row is None:
                raise RuntimeError(f"hook disappeared during validation: {target.hook}")
            config = await repo.binding_config(conn, target.tenant_id, target.hook_id)
        hook = HookDefOut(
            name=hook_row.name,
            def_version=hook_row.def_version,
            **hook_row.spec.model_dump(),
        )
        return hook, config

    def _jitter(self, attempt: int) -> float:
        cap = min(
            self._backoff_max_s,
            self._backoff_base_s * (2 ** min(attempt, 30)),
        )
        return random.uniform(0, cap)  # noqa: S311 - retry jitter is not security-sensitive
