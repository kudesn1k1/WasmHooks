"""A thin client of the data plane's Invoke API (api/invoke.openapi.yaml).

It reports what happened and decides nothing: the fallback policy lives at
the call site (the checkout). The Python SDK (T8) replaces it.
"""

import asyncio
from enum import StrEnum
from typing import Any
from urllib.parse import quote

import httpx
from pydantic import BaseModel, ValidationError


class Outcome(StrEnum):
    OK = "ok"
    NO_HANDLER = "no_handler"
    TIMEOUT = "timeout"
    HANDLER_ERROR = "handler_error"
    QUOTA_EXCEEDED = "quota_exceeded"
    UNAVAILABLE = "unavailable"


class InvokeResponse(BaseModel):
    """The fields the shop reads. Unknown fields are ignored (tolerant reader)."""

    outcome: Outcome
    result: dict[str, Any] | None = None
    error: str | None = None
    duration_ms: int | None = None
    module_hash: str | None = None


class InvokeFailed(Exception):
    """No outcome. This includes 502 when Execute may have run but its answer was lost."""


# Statuses whose body is an InvokeResponse; any other is a problem+json.
_OUTCOME_STATUSES = frozenset({200, 429, 503})
# The data plane gets a slightly shorter deadline than the caller's own cutoff,
# so that its answer (a timeout included) still arrives in time.
_NETWORK_MARGIN_MS = 20


class InvokeClient:
    def __init__(
        self, base_url: str, api_key: str, transport: httpx.AsyncBaseTransport | None = None
    ) -> None:
        self._http = httpx.AsyncClient(
            base_url=base_url,
            headers={"Authorization": f"Bearer {api_key}"},
            transport=transport,
            # The data plane is internal: never route it through HTTP_PROXY.
            trust_env=False,
        )

    async def invoke(
        self, hook: str, tenant_id: str, payload: dict[str, Any], deadline_ms: int
    ) -> InvokeResponse:
        body = {
            "tenant_id": tenant_id,
            "payload": payload,
            "deadline_ms": max(1, deadline_ms - _NETWORK_MARGIN_MS),
        }
        deadline_s = deadline_ms / 1000
        try:
            # httpx timeouts are per phase; asyncio.timeout bounds the whole call.
            async with asyncio.timeout(deadline_s):
                response = await self._http.post(
                    f"/v1/hooks/{quote(hook, safe='')}/invoke", json=body, timeout=deadline_s
                )
        except TimeoutError as exc:
            raise InvokeFailed(f"no answer within {deadline_ms} ms") from exc
        except httpx.HTTPError as exc:
            # Transport failures and bodies that cannot be decoded alike.
            raise InvokeFailed(f"request to the data plane failed: {exc!r}") from exc
        if response.status_code not in _OUTCOME_STATUSES:
            raise InvokeFailed(f"data plane answered {response.status_code}: {response.text[:500]}")
        try:
            return InvokeResponse.model_validate_json(response.content)
        except ValidationError as exc:
            raise InvokeFailed(
                f"data plane answered {response.status_code} with a malformed body: {exc}"
            ) from exc

    async def aclose(self) -> None:
        await self._http.aclose()
