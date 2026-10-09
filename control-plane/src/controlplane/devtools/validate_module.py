"""Dev tool: upload a wasm file to storage and ask the data plane to validate it."""

from pathlib import Path

import anyio

from controlplane.db import create_engine
from controlplane.devtools.hook_lookup import load_hook
from controlplane.dpclient.client import DataPlaneClient
from controlplane.dpclient.schemas import ValidationReport
from controlplane.settings import Settings
from controlplane.storage.s3 import ModuleStorage


async def validate_module(
    settings: Settings,
    path: Path,
    hook_name: str,
    *,
    storage: ModuleStorage | None = None,
    client: DataPlaneClient | None = None,
) -> ValidationReport:
    """Raises ValueError if the hook does not exist."""
    storage = storage or ModuleStorage.from_settings(settings)
    engine = create_engine(settings.database_url)
    try:
        async with engine.connect() as conn:
            _, hook = await load_hook(conn, hook_name)
    finally:
        await engine.dispose()
    hash_ = await storage.put_module(await anyio.Path(path).read_bytes())
    if client is not None:
        return await client.validate(hash_, hook, None)
    own = DataPlaneClient(
        settings.dataplane_internal_url, settings.internal_token.get_secret_value()
    )
    try:
        return await own.validate(hash_, hook, None)
    finally:
        await own.aclose()
