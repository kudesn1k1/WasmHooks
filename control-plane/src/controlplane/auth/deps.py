import hmac
from typing import Annotated

from fastapi import Depends
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer

from controlplane.errors import ProblemError

UNAUTHORIZED = "Не авторизован"

_internal_bearer = HTTPBearer(auto_error=False, scheme_name="internalToken")


class InternalAuth:
    """Guards /internal/*: the token shared by the control plane and the data
    plane. It is not an operator API key."""

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
