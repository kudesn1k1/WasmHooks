"""Installation API keys (decision D19).

A key is shown once, when created; only its SHA-256 is stored. The same keys
authenticate the operator API here and Invoke calls in the data plane, which
receives the hashes in the snapshot.
"""

import hashlib
import secrets
import string
from dataclasses import dataclass

from sqlalchemy import insert
from sqlalchemy.ext.asyncio import AsyncConnection

from controlplane.configstate.version import bump_config_version
from controlplane.tables import api_keys

TOKEN_PREFIX = "whk_"  # noqa: S105 - a public prefix, not a secret
TOKEN_LENGTH = 32  # base62 characters, ~190 bits
_BASE62 = string.digits + string.ascii_letters


@dataclass(frozen=True)
class GeneratedKey:
    id: str
    name: str
    token: str
    sha256: str


def hash_token(token: str) -> str:
    return hashlib.sha256(token.encode()).hexdigest()


def generate_api_key(name: str) -> GeneratedKey:
    token = TOKEN_PREFIX + "".join(secrets.choice(_BASE62) for _ in range(TOKEN_LENGTH))
    return GeneratedKey(
        id="key_" + secrets.token_hex(8), name=name, token=token, sha256=hash_token(token)
    )


async def create_api_key(conn: AsyncConnection, name: str) -> GeneratedKey:
    """Stores a new key in the caller's transaction and bumps the snapshot version."""
    key = generate_api_key(name)
    await conn.execute(insert(api_keys).values(id=key.id, name=key.name, sha256=key.sha256))
    await bump_config_version(conn, "api_key.created", {"id": key.id, "name": key.name})
    return key
