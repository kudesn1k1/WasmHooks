"""Module storage: wasm binaries in S3, addressed by their sha256.

boto3 is synchronous, so every call runs in a worker thread.
"""

import hashlib
from functools import partial
from typing import Any

import anyio
import boto3
import botocore.config
from botocore.exceptions import ClientError

from controlplane.settings import Settings

HASH_PREFIX = "sha256:"


def module_hash(data: bytes) -> str:
    return HASH_PREFIX + hashlib.sha256(data).hexdigest()


_hash_of = module_hash  # get_module has a parameter that shadows the name


def _object_key(hash_: str) -> str:
    return hash_.removeprefix(HASH_PREFIX) + ".wasm"


class ModuleNotFound(Exception):
    pass


class ModuleHashMismatch(Exception):
    pass


class ModuleStorage:
    def __init__(
        self, endpoint: str, bucket: str, access_key: str, secret_key: str, region: str
    ) -> None:
        self._bucket = bucket
        self._s3: Any = boto3.client(
            "s3",
            endpoint_url=endpoint,
            aws_access_key_id=access_key,
            aws_secret_access_key=secret_key,
            region_name=region,
            config=botocore.config.Config(
                signature_version="s3v4", s3={"addressing_style": "path"}
            ),
        )

    @classmethod
    def from_settings(cls, settings: Settings) -> "ModuleStorage":
        return cls(
            endpoint=settings.s3_endpoint,
            bucket=settings.s3_bucket,
            access_key=settings.s3_access_key.get_secret_value(),
            secret_key=settings.s3_secret_key.get_secret_value(),
            region=settings.s3_region,
        )

    async def put_module(self, data: bytes) -> str:
        """Stores the bytes under their hash and returns it. Same bytes, same key:
        a repeated put rewrites identical content."""
        hash_ = module_hash(data)
        await anyio.to_thread.run_sync(
            partial(self._s3.put_object, Bucket=self._bucket, Key=_object_key(hash_), Body=data)
        )
        return hash_

    async def get_module(self, module_hash: str) -> bytes:
        try:
            body = await anyio.to_thread.run_sync(self._get, _object_key(module_hash))
        except ClientError as exc:
            if exc.response.get("Error", {}).get("Code") in ("NoSuchKey", "404"):
                raise ModuleNotFound(module_hash) from exc
            raise
        actual = _hash_of(body)
        if actual != module_hash:
            raise ModuleHashMismatch(f"expected {module_hash}, object has {actual}")
        return body

    def _get(self, key: str) -> bytes:
        response = self._s3.get_object(Bucket=self._bucket, Key=key)
        data: bytes = response["Body"].read()
        return data
