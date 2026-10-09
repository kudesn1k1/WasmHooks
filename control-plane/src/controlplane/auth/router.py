from datetime import datetime
from typing import Annotated

from fastapi import APIRouter, Depends
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.auth.deps import OperatorAuth, OperatorPrincipal
from controlplane.auth.tenant_tokens import TenantTokens
from controlplane.errors import ProblemError
from controlplane.tables import tenants


class ConsoleSession(BaseModel):
    token: str
    expires_at: datetime


def build_router(engine: AsyncEngine, tokens: TenantTokens, operator: OperatorAuth) -> APIRouter:
    router = APIRouter(tags=["tenant console sessions"])

    @router.post(
        "/api/v1/tenants/{external_id}/console-sessions",
        status_code=201,
        response_model=ConsoleSession,
        summary="Выписать токен консоли тенанта",
        description=(
            "Бэкенд оператора выписывает короткоживущий токен для своего тенанта и передаёт "
            "его в браузер тенанта; консоль тенанта ходит с ним как с Bearer-токеном "
            "(решение D18)."
        ),
    )
    async def create_console_session(
        external_id: str, _: Annotated[OperatorPrincipal, Depends(operator)]
    ) -> ConsoleSession:
        async with engine.connect() as conn:
            exists = (
                await conn.execute(select(tenants.c.id).where(tenants.c.external_id == external_id))
            ).first()
        if exists is None:
            raise ProblemError(404, "Тенант не найден", external_id)
        issued = tokens.issue(external_id)
        return ConsoleSession(token=issued.token, expires_at=issued.expires_at)

    return router
