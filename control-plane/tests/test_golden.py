"""Golden snapshot shared with the data plane.

The control plane builds a snapshot from a fixed data set and compares it with
api/testdata/snapshot.golden.json; the data plane's
internal/config/golden_test.go parses the same file. Together they prove the
two languages agree on the format (decision D22). Regenerate deliberately:
UPDATE_GOLDEN=1 uv run pytest tests/test_golden.py
"""

import json
import os

import httpx
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.configstate.version import bump_config_version
from tests.factories import (
    REPO_ROOT,
    add_api_key,
    add_binding,
    add_hook,
    add_module,
    add_tenant,
    wasm_hash,
)

GOLDEN = REPO_ROOT / "api" / "testdata" / "snapshot.golden.json"


async def test_golden_snapshot(client: httpx.AsyncClient, engine: AsyncEngine) -> None:
    async with engine.begin() as conn:
        await add_api_key(conn, "key_golden", "golden", "whk_golden")
        hook = await add_hook(conn, "checkout.discount")
        tenant = await add_tenant(conn, "merchant-a", concurrency_limit=4, rate_limit_rps=100)
        await add_tenant(conn, "merchant-z")
        module = await add_module(conn, tenant, hook, wasm_hash("discount.wasm"))
        await add_binding(conn, tenant, hook, module, {"threshold": "1000", "percent": "10"})
        await bump_config_version(conn, "test.golden", {})

    resp = await client.get(
        "/internal/v1/config/snapshot", headers={"Authorization": "Bearer test-internal-token"}
    )
    assert resp.status_code == 200
    got = resp.json()

    if os.environ.get("UPDATE_GOLDEN") == "1":
        GOLDEN.parent.mkdir(parents=True, exist_ok=True)
        text = json.dumps(got, indent=2, ensure_ascii=False) + "\n"
        GOLDEN.write_text(text, encoding="utf-8", newline="\n")
    assert got == json.loads(GOLDEN.read_text(encoding="utf-8"))
