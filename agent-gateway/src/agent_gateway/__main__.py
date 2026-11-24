"""Entry point: ``python -m agent_gateway`` runs the MCP server over STDIO."""

from __future__ import annotations

import logging
import sys

from .config import ConfigError, GatewayConfig
from .server import Gateway, build_server


def main() -> None:
    # stdout carries the MCP protocol in STDIO transport: every log goes to stderr.
    logging.basicConfig(stream=sys.stderr, level=logging.WARNING)
    try:
        config = GatewayConfig.from_env()
    except ConfigError as exc:
        print(f"agent-gateway: configuration error: {exc}", file=sys.stderr)
        raise SystemExit(2) from None
    build_server(Gateway(config)).run("stdio")


if __name__ == "__main__":
    main()
