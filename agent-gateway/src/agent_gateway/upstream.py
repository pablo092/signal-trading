"""HTTP client for the backing services, restricted to an allowlist of read endpoints.

The gateway holds no trading logic (ADR-002). It can only reach the endpoints in
``ALLOWED_ENDPOINTS``; anything else is refused before a request is made. Upstream
errors are mapped to short, sanitized messages: raw broker errors never reach the agent.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Literal

import httpx

from .config import GatewayConfig

Service = Literal["signal", "engine"]

ALLOWED_ENDPOINTS: frozenset[tuple[Service, str, str]] = frozenset(
    {
        ("signal", "GET", "/api/v1/signals/strategies"),
        ("signal", "POST", "/api/v1/signals/generate"),
        ("engine", "GET", "/api/v1/portfolio/equity"),
        ("engine", "GET", "/api/v1/portfolio/positions"),
        ("engine", "GET", "/api/v1/trading-mode"),
    }
)

_SERVICE_NAMES: dict[Service, str] = {"signal": "signal-service", "engine": "bot-engine"}


@dataclass
class UpstreamError(Exception):
    """A sanitized upstream failure. ``message`` is safe to show to the agent."""

    code: str
    message: str
    status: int | None = None

    def __str__(self) -> str:
        return f"{self.code}: {self.message}"


class Upstream:
    def __init__(self, config: GatewayConfig, transport: httpx.AsyncBaseTransport | None = None) -> None:
        self._base: dict[Service, str] = {
            "signal": config.signal_service_url,
            "engine": config.bot_engine_url,
        }
        self._client = httpx.AsyncClient(
            timeout=config.request_timeout_seconds,
            headers={
                "Authorization": f"Bearer {config.gateway_token}",
                "User-Agent": "signal-trading-agent-gateway",
            },
            transport=transport,
            follow_redirects=False,
        )

    async def aclose(self) -> None:
        await self._client.aclose()

    async def request(
        self, service: Service, method: str, path: str, json: dict[str, Any] | None = None
    ) -> Any:
        if (service, method, path) not in ALLOWED_ENDPOINTS:
            raise UpstreamError("endpoint_not_allowed", f"{method} {path} is not a read endpoint")
        name = _SERVICE_NAMES[service]
        try:
            resp = await self._client.request(method, self._base[service] + path, json=json)
        except httpx.TimeoutException:
            raise UpstreamError("upstream_timeout", f"{name} did not answer in time") from None
        except httpx.HTTPError:
            raise UpstreamError("upstream_unreachable", f"{name} is not reachable") from None

        if resp.status_code == 200:
            try:
                return resp.json()
            except ValueError:
                raise UpstreamError("upstream_bad_response", f"{name} returned invalid JSON", 200) from None
        raise _map_error(name, resp)


def _map_error(name: str, resp: httpx.Response) -> UpstreamError:
    status = resp.status_code
    if status == 401:
        return UpstreamError(
            "gateway_unauthorized", f"{name} rejected the gateway token (check GATEWAY_TOKEN)", status
        )
    if status == 403:
        scope = _detail_field(resp, "required_scope")
        suffix = f" (requires {scope})" if scope else ""
        return UpstreamError("forbidden", f"the gateway token lacks permission{suffix}", status)
    if status == 422:
        detail = _detail_field(resp, None)
        # signal-service validation messages are safe, short strings; pydantic lists are summarized.
        msg = detail if isinstance(detail, str) and len(detail) <= 300 else "invalid input"
        return UpstreamError("invalid_input", msg, status)
    if status == 429:
        return UpstreamError("upstream_rate_limited", f"{name} is rate limiting requests", status)
    if status == 503:
        return UpstreamError("market_data_unavailable", "market data is temporarily unavailable", status)
    return UpstreamError("upstream_error", f"{name} returned HTTP {status}", status)


def _detail_field(resp: httpx.Response, key: str | None) -> Any:
    try:
        body = resp.json()
    except ValueError:
        return None
    if not isinstance(body, dict):
        return None
    if key is None:
        return body.get("detail")
    if key in body:
        return body[key]
    detail = body.get("detail")
    return detail.get(key) if isinstance(detail, dict) else None
