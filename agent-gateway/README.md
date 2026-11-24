# agent-gateway

Read-only MCP server (STDIO) that lets AI agents analyze the market through signal-trading
(ADR-002, M1). It holds no trading logic: each tool maps to one read endpoint of
signal-service or bot-engine.

| Tool | Backend | Notes |
|---|---|---|
| `list_strategies` | `GET signal-service /api/v1/signals/strategies` | |
| `generate_signal(symbol, strategy="rsi", timeframe="1Hour", bars=100)` | `POST signal-service /api/v1/signals/generate` | Uses Alpaca market data quota |
| `get_portfolio_summary` | `GET bot-engine /api/v1/portfolio/equity` | Paper account |
| `get_positions` | `GET bot-engine /api/v1/portfolio/positions` | |
| `get_trading_mode` | `GET bot-engine /api/v1/trading-mode` | Verified paper mode, execution flag |

**There is no order capability.** The gateway can only reach the endpoints in
`ALLOWED_ENDPOINTS` ([upstream.py](src/agent_gateway/upstream.py)). Its service token
holds only `market:read` and `portfolio:read`. bot-engine refuses to load an agent token
with a write scope, and answers 403 on the order route even when execution is enabled.

## Safety properties

- **Input validation.** Symbols and strategy names are validated before any request.
- **Sanitized errors.** Raw broker errors and unexpected fields never reach the agent.
- **No redirects.** The gateway never follows redirects. Plain `http` is allowed only to `localhost`, so the token never leaves the host in clear text.
- **Rate limit.** Calls are limited per agent (`RATE_LIMIT_PER_MINUTE`, default 30).
- **Audit.** Each tool call writes one JSON line to **stderr**, plus `AUDIT_LOG_FILE` if set. The line holds the actor, tool, arguments, outcome, error code and duration, and never the token. stdout is reserved for the MCP protocol.

## Setup

1. Start signal-service and bot-engine (see the root README).
2. Issue a gateway token and add it to the shared table, following [SERVICE_TOKENS.md](../docs/runbooks/SERVICE_TOKENS.md). Use type `agent` and scopes `market:read` and `portfolio:read`.
3. Install the gateway with Python 3.11+:
   ```sh
   cd agent-gateway
   py -3.11 -m venv .venv                         # Linux/macOS: python3.11 -m venv .venv
   .venv/Scripts/python -m pip install --require-hashes -r requirements.lock
   .venv/Scripts/python -m pip install --no-deps -e .
   ```

## Connect an MCP client

| Variable | Default | |
|---|---|---|
| `GATEWAY_TOKEN` | **required** | Plaintext gateway token. Keep it out of git. |
| `AGENT_ID` | `mcp-agent` | Recorded in every audit line |
| `SIGNAL_SERVICE_URL` | `http://127.0.0.1:8000` | |
| `BOT_ENGINE_URL` | `http://127.0.0.1:8080` | |
| `RATE_LIMIT_PER_MINUTE` | `30` | |
| `REQUEST_TIMEOUT_SECONDS` | `15` | |
| `AUDIT_LOG_FILE` | (none) | Append-only JSONL |

**Claude Code** (`.mcp.json` in your project; do not commit the token):
```json
{
  "mcpServers": {
    "signal-trading": {
      "command": "C:/path/to/signal-trading/agent-gateway/.venv/Scripts/python.exe",
      "args": ["-m", "agent_gateway"],
      "env": { "GATEWAY_TOKEN": "<token>", "AGENT_ID": "claude-code" }
    }
  }
}
```

**Codex CLI** (`~/.codex/config.toml`):
```toml
[mcp_servers.signal-trading]
command = "C:/path/to/signal-trading/agent-gateway/.venv/Scripts/python.exe"
args = ["-m", "agent_gateway"]
env = { GATEWAY_TOKEN = "<token>", AGENT_ID = "codex" }
```

## Development

```sh
.venv/Scripts/python -m pip install --require-hashes -r requirements-dev.lock
.venv/Scripts/python -m pip install --no-deps -e .
.venv/Scripts/python -m pytest --cov=agent_gateway
.venv/Scripts/python -m ruff check src tests
.venv/Scripts/python -m mypy src
```

The tests drive the server through a real MCP client: in-process for the tool tests,
and as a `python -m agent_gateway` subprocess over STDIO for the end-to-end test.
After changing dependencies, regenerate the locks with `uv pip compile` (see
[signal-service/README.md](../signal-service/README.md)).
