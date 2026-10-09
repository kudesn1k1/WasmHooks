"""Client for the data plane's internal API (api/dataplane-internal.openapi.yaml)."""

from typing import Any

import httpx

from controlplane.configstate.schemas import HookDefOut
from controlplane.dpclient.schemas import ValidationReport


class DataPlaneUnavailable(Exception):
    """503, a connection failure or a timeout: worth retrying later."""


class DataPlaneError(Exception):
    """Any other non-200 answer."""


class DataPlaneClient:
    def __init__(
        self,
        base_url: str,
        token: str,
        timeout_s: float = 30.0,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self._http = httpx.AsyncClient(
            base_url=base_url,
            headers={"Authorization": f"Bearer {token}"},
            timeout=timeout_s,
            transport=transport,
        )

    async def validate(
        self, module_hash: str, hook: HookDefOut, config: dict[str, str] | None = None
    ) -> ValidationReport:
        body: dict[str, Any] = {"module_hash": module_hash, "hook": hook.model_dump(mode="json")}
        if config is not None:
            body["config"] = config
        try:
            response = await self._http.post("/internal/v1/modules/validate", json=body)
        except (httpx.ConnectError, httpx.TimeoutException) as exc:
            raise DataPlaneUnavailable(f"data plane unreachable: {exc!r}") from exc
        if response.status_code == 503:
            raise DataPlaneUnavailable("data plane answered 503")
        if response.status_code != 200:
            raise DataPlaneError(f"data plane answered {response.status_code}: {response.text}")
        return ValidationReport.model_validate_json(response.content)

    async def aclose(self) -> None:
        await self._http.aclose()
