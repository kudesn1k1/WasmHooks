from typing import Any

import boto3
import pytest

from controlplane.storage.s3 import (
    ModuleHashMismatch,
    ModuleNotFound,
    ModuleStorage,
    module_hash,
)


def make_storage(minio: dict[str, str]) -> ModuleStorage:
    return ModuleStorage(**minio)


def raw_s3(minio: dict[str, str]) -> Any:
    return boto3.client(
        "s3",
        endpoint_url=minio["endpoint"],
        aws_access_key_id=minio["access_key"],
        aws_secret_access_key=minio["secret_key"],
        region_name=minio["region"],
    )


def test_module_hash_format() -> None:
    assert module_hash(b"") == (
        "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
    )


async def test_put_stores_object_under_hash(minio: dict[str, str]) -> None:
    data = b"\x00asm-put"
    got = await make_storage(minio).put_module(data)
    assert got == module_hash(data)
    key = got.removeprefix("sha256:") + ".wasm"
    body = raw_s3(minio).get_object(Bucket=minio["bucket"], Key=key)["Body"].read()
    assert body == data


async def test_put_twice_is_not_an_error(minio: dict[str, str]) -> None:
    storage = make_storage(minio)
    assert await storage.put_module(b"same") == await storage.put_module(b"same")


async def test_get_returns_bytes(minio: dict[str, str]) -> None:
    storage = make_storage(minio)
    hash_ = await storage.put_module(b"round trip")
    assert await storage.get_module(hash_) == b"round trip"


async def test_get_unknown_hash(minio: dict[str, str]) -> None:
    with pytest.raises(ModuleNotFound):
        await make_storage(minio).get_module(module_hash(b"never stored"))


async def test_get_detects_overwritten_object(minio: dict[str, str]) -> None:
    storage = make_storage(minio)
    hash_ = await storage.put_module(b"original")
    raw_s3(minio).put_object(
        Bucket=minio["bucket"], Key=hash_.removeprefix("sha256:") + ".wasm", Body=b"tampered"
    )
    with pytest.raises(ModuleHashMismatch):
        await storage.get_module(hash_)
