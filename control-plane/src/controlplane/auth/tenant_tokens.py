"""Tenant console tokens (decision D18).

The operator's backend asks for a short-lived token for one of its tenants and
hands it to the tenant's browser; the tenant console sends it as a bearer
token. The platform keeps no tenant passwords: the operator already knows who
its tenants are. Verification needs no database: signature, audience, issuer
and expiry are enough. Revocation is by expiry only (known M1 limitation).
"""

from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta

import jwt

from controlplane.errors import ProblemError

UNAUTHORIZED = "Не авторизован"


@dataclass(frozen=True)
class IssuedToken:
    token: str
    expires_at: datetime


def _utcnow() -> datetime:
    return datetime.now(UTC)


class TenantTokens:
    AUDIENCE = "tenant-console"
    ISSUER = "wasmhooks-control-plane"

    def __init__(self, secret: str, ttl_s: int, now: Callable[[], datetime] = _utcnow) -> None:
        self._secret = secret
        self._ttl = timedelta(seconds=ttl_s)
        self._now = now

    def issue(self, external_id: str) -> IssuedToken:
        now = self._now().replace(microsecond=0)
        expires_at = now + self._ttl
        claims = {
            "sub": external_id,
            "aud": self.AUDIENCE,
            "iss": self.ISSUER,
            "iat": int(now.timestamp()),
            "exp": int(expires_at.timestamp()),
        }
        return IssuedToken(jwt.encode(claims, self._secret, algorithm="HS256"), expires_at)

    def verify(self, token: str) -> str:
        """Returns the tenant's external_id; any problem is a plain 401."""
        try:
            claims = jwt.decode(
                token,
                self._secret,
                algorithms=["HS256"],
                audience=self.AUDIENCE,
                issuer=self.ISSUER,
                options={"require": ["exp", "iat", "sub", "aud", "iss"]},
            )
        except jwt.PyJWTError as exc:
            raise ProblemError(401, UNAUTHORIZED) from exc
        return str(claims["sub"])
