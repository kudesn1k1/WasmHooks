"""FastAPI dependencies for the three kinds of caller.

- InternalAuth: the data plane, with the token shared between the planes.
- OperatorAuth: the operator, with an installation API key.
- TenantAuth: a tenant console, with a token the operator issued.

All failures are the same plain 401, without saying what was wrong.
"""

import hmac
from dataclasses import dataclass
from typing import Annotated

from fastapi import Depends
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.auth.keys import hash_token
from controlplane.auth.tenant_tokens import UNAUTHORIZED, TenantTokens
from controlplane.errors import ProblemError
from controlplane.tables import api_keys

# Separate schemes so the generated OpenAPI says which credential each
# endpoint takes.
_internal_bearer = HTTPBearer(auto_error=False, scheme_name="internalToken")
_operator_bearer = HTTPBearer(auto_error=False, scheme_name="operatorKey")
_tenant_bearer = HTTPBearer(auto_error=False, scheme_name="tenantToken")


@dataclass(frozen=True)
class OperatorPrincipal:
    key_id: str


@dataclass(frozen=True)
class TenantPrincipal:
    external_id: str


class InternalAuth:
    """Guards /internal/*. The token is not an operator API key."""

    def __init__(self, token: str) -> None:
        self._token = token.encode()

    async def __call__(
        self,
        credentials: Annotated[HTTPAuthorizationCredentials | None, Depends(_internal_bearer)],
    ) -> None:
        if credentials is None or not hmac.compare_digest(
            credentials.credentials.encode(), self._token
        ):
            raise ProblemError(401, UNAUTHORIZED)


class OperatorAuth:
    def __init__(self, engine: AsyncEngine) -> None:
        self._engine = engine

    async def __call__(
        self,
        credentials: Annotated[HTTPAuthorizationCredentials | None, Depends(_operator_bearer)],
    ) -> OperatorPrincipal:
        if credentials is None:
            raise ProblemError(401, UNAUTHORIZED)
        # Lookup by hash: the stored value is a SHA-256, so an index lookup
        # leaks nothing useful about the key through timing.
        async with self._engine.connect() as conn:
            key_id = (
                await conn.execute(
                    select(api_keys.c.id).where(
                        api_keys.c.sha256 == hash_token(credentials.credentials),
                        api_keys.c.revoked_at.is_(None),
                    )
                )
            ).scalar_one_or_none()
        if key_id is None:
            raise ProblemError(401, UNAUTHORIZED)
        return OperatorPrincipal(key_id=key_id)


class TenantAuth:
    def __init__(self, tokens: TenantTokens) -> None:
        self._tokens = tokens

    async def __call__(
        self,
        credentials: Annotated[HTTPAuthorizationCredentials | None, Depends(_tenant_bearer)],
    ) -> TenantPrincipal:
        if credentials is None:
            raise ProblemError(401, UNAUTHORIZED)
        return TenantPrincipal(external_id=self._tokens.verify(credentials.credentials))
