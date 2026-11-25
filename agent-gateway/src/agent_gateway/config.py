"""Gateway configuration from environment variables. Invalid configuration is fatal."""

from __future__ import annotations

import os
import re
from dataclasses import dataclass, field
from urllib.parse import urlsplit

_AGENT_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")
_LOOPBACK = {"localhost", "127.0.0.1", "::1"}


class ConfigError(ValueError):
    """Invalid gateway configuration. Messages never include secret values."""


def _check_url(name: str, url: str) -> str:
    parts = urlsplit(url)
    if parts.scheme not in ("http", "https") or not parts.hostname:
        raise ConfigError(f"{name} must be an http(s) URL")
    if parts.username or parts.password:
        raise ConfigError(f"{name} must not contain credentials")
    # The service token travels in a header: never send it in clear text off the host.
    if parts.scheme == "http" and parts.hostname not in _LOOPBACK:
        raise ConfigError(f"{name} must use https unless it points to localhost")
    return url.rstrip("/")


def _positive_float(name: str, raw: str) -> float:
    try:
        value = float(raw)
    except ValueError:
        raise ConfigError(f"{name} must be a number") from None
    if value <= 0:
        raise ConfigError(f"{name} must be > 0")
    return value


@dataclass(frozen=True)
class GatewayConfig:
    gateway_token: str = field(repr=False)
    agent_id: str = "mcp-agent"
    signal_service_url: str = "http://127.0.0.1:8000"
    bot_engine_url: str = "http://127.0.0.1:8080"
    rate_limit_per_minute: float = 30.0
    request_timeout_seconds: float = 15.0
    audit_log_file: str = ""

    @classmethod
    def from_env(cls, env: dict[str, str] | None = None) -> GatewayConfig:
        e = dict(os.environ) if env is None else env
        token = e.get("GATEWAY_TOKEN", "").strip()
        if not token:
            raise ConfigError(
                "GATEWAY_TOKEN is required (a read-only service token, see "
                "docs/runbooks/SERVICE_TOKENS.md)"
            )
        agent_id = e.get("AGENT_ID", "mcp-agent").strip()
        if not _AGENT_ID_RE.match(agent_id):
            raise ConfigError("AGENT_ID must be 1-64 chars of letters, digits, '.', '_' or '-'")
        return cls(
            gateway_token=token,
            agent_id=agent_id,
            signal_service_url=_check_url(
                "SIGNAL_SERVICE_URL", e.get("SIGNAL_SERVICE_URL", "http://127.0.0.1:8000")
            ),
            bot_engine_url=_check_url(
                "BOT_ENGINE_URL", e.get("BOT_ENGINE_URL", "http://127.0.0.1:8080")
            ),
            rate_limit_per_minute=_positive_float(
                "RATE_LIMIT_PER_MINUTE", e.get("RATE_LIMIT_PER_MINUTE", "30")
            ),
            request_timeout_seconds=_positive_float(
                "REQUEST_TIMEOUT_SECONDS", e.get("REQUEST_TIMEOUT_SECONDS", "15")
            ),
            audit_log_file=e.get("AUDIT_LOG_FILE", "").strip(),
        )
