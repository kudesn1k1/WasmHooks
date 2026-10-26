from typing import Annotated

from fastapi import APIRouter, Depends, Query, Response

from controlplane.auth.deps import InternalAuth
from controlplane.configstate.snapshot import SnapshotService
from controlplane.configstate.watcher import VersionWatcher

MAX_INT64 = 2**63 - 1


def build_router(
    snapshots: SnapshotService, watcher: VersionWatcher, auth: InternalAuth
) -> APIRouter:
    router = APIRouter(tags=["internal"], dependencies=[Depends(auth)])

    # Not part of the console contract: this endpoint is specified in
    # api/dataplane-internal.openapi.yaml and checked by test_snapshot_contract.py.
    @router.get("/internal/v1/config/snapshot", include_in_schema=False)
    async def get_snapshot(
        after_version: Annotated[int | None, Query(ge=0, le=MAX_INT64)] = None,
        wait_s: Annotated[int, Query(ge=1, le=60)] = 30,
    ) -> Response:
        if after_version is not None and watcher.current() <= after_version:
            if not await watcher.wait_newer(after_version, wait_s):
                return Response(status_code=304)
        version, body = await snapshots.current()
        if after_version is not None and version <= after_version:
            return Response(status_code=304)
        return Response(content=body, media_type="application/json")

    return router
