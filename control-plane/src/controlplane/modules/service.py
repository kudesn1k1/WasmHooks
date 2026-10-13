"""Tenant module upload flow: object storage first, metadata second."""

from fastapi import UploadFile
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.errors import ProblemError
from controlplane.hooks import repo as hooks_repo
from controlplane.hooks.service import NOT_FOUND as HOOK_NOT_FOUND
from controlplane.modules import repo
from controlplane.modules.schemas import Module
from controlplane.modules.validator import ModuleValidator
from controlplane.storage.s3 import ModuleStorage
from controlplane.tenants import repo as tenants_repo


def _to_module(row: repo.ModuleRow) -> Module:
    return Module(
        id=str(row.id),
        hook=row.hook,
        content_hash=row.content_hash,
        size_bytes=row.size_bytes,
        status=row.status,
        validation_report=row.validation_report,
        uploaded_at=row.uploaded_at,
    )


class ModuleService:
    def __init__(
        self,
        engine: AsyncEngine,
        storage: ModuleStorage,
        max_module_bytes: int,
        validator: ModuleValidator,
    ) -> None:
        self._engine = engine
        self._storage = storage
        self._max_module_bytes = max_module_bytes
        self._validator = validator

    async def list_all(self, external_id: str, hook: str) -> list[Module]:
        async with self._engine.connect() as conn:
            tenant_row = await tenants_repo.get_tenant(conn, external_id)
            hook_row = await hooks_repo.get_hook(conn, hook)
            if hook_row is None:
                raise ProblemError(404, HOOK_NOT_FOUND, hook)
            if tenant_row is None:
                raise ProblemError(404, "Тенант не найден", external_id)
            rows = await repo.list_modules(conn, tenant_row.id, hook_row.id, hook)
        return [_to_module(row) for row in rows]

    async def upload(self, external_id: str, hook: str, file: UploadFile) -> Module:
        async with self._engine.connect() as conn:
            tenant_row = await tenants_repo.get_tenant(conn, external_id)
            hook_row = await hooks_repo.get_hook(conn, hook)
        if hook_row is None:
            raise ProblemError(404, HOOK_NOT_FOUND, hook)
        if tenant_row is None:
            raise ProblemError(404, "Тенант не найден", external_id)

        data = await file.read(self._max_module_bytes + 1)
        if len(data) > self._max_module_bytes:
            raise ProblemError(413, "Модуль слишком большой")
        if not data:
            raise ProblemError(422, "Пустой модуль")

        # An orphaned object is harmless; a metadata row without its object
        # would make validation impossible, so S3 must complete first.
        content_hash = await self._storage.put_module(data)
        async with self._engine.begin() as conn:
            row = await repo.insert_module(
                conn,
                tenant_row.id,
                hook_row.id,
                hook,
                content_hash,
                len(data),
            )
            if row is None:
                raise ProblemError(409, "Такая версия уже загружена", content_hash)
        self._validator.enqueue(row.id)
        return _to_module(row)
