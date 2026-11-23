"""Client of the control plane's operator API (api/control-plane.openapi.yaml):
registering a merchant as a tenant and issuing its console token."""

from datetime import datetime
from urllib.parse import quote

import httpx
from pydantic import BaseModel, ValidationError


class ConsoleSession(BaseModel):
    token: str
    expires_at: datetime


class ConsoleUnavailable(Exception):
    """The control plane did not do what was asked; the message says why."""


class ControlPlaneClient:
    def __init__(
        self,
        base_url: str,
        api_key: str,
        timeout_s: float,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self._http = httpx.AsyncClient(
            base_url=base_url,
            headers={"Authorization": f"Bearer {api_key}"},
            timeout=timeout_s,
            transport=transport,
            # The control plane is internal: never route it through HTTP_PROXY.
            trust_env=False,
        )

    async def ensure_tenant(self, tenant_id: str, name: str) -> None:
        """Registers the merchant as a tenant; one that already exists is fine."""
        response = await self._post("/api/v1/tenants", {"external_id": tenant_id, "name": name})
        if response.status_code not in (201, 409):
            raise ConsoleUnavailable(
                f"creating tenant {tenant_id}: control plane answered "
                f"{response.status_code}: {response.text[:500]}"
            )

    async def console_session(self, tenant_id: str) -> ConsoleSession:
        response = await self._post(
            f"/api/v1/tenants/{quote(tenant_id, safe='')}/console-sessions", None
        )
        if response.status_code != 201:
            raise ConsoleUnavailable(
                f"console session for {tenant_id}: control plane answered "
                f"{response.status_code}: {response.text[:500]}"
            )
        try:
            return ConsoleSession.model_validate_json(response.content)
        except ValidationError as exc:
            raise ConsoleUnavailable(f"control plane answered a malformed session: {exc}") from exc

    async def _post(self, path: str, body: dict[str, str] | None) -> httpx.Response:
        try:
            return await self._http.post(path, json=body)
        except httpx.HTTPError as exc:
            # Transport failures and bodies that cannot be decoded alike.
            raise ConsoleUnavailable(f"request to the control plane failed: {exc!r}") from exc

    async def aclose(self) -> None:
        await self._http.aclose()
