# agent-gateway

MCP server (STDIO) that lets AI agents analyze the market through signal-trading and
**propose** paper orders (ADR-002, BL-26). It holds no trading logic: each tool maps to one
endpoint of signal-service or bot-engine.

| Tool | Backend | Notes |
|---|---|---|
| `list_strategies` | `GET signal-service /api/v1/signals/strategies` | |
| `generate_signal(symbol, strategy="rsi", timeframe="1Hour", bars=100)` | `POST signal-service /api/v1/signals/generate` | Uses Alpaca market data quota |
| `get_portfolio_summary` | `GET bot-engine /api/v1/portfolio/equity` | Paper account |
| `get_positions` | `GET bot-engine /api/v1/portfolio/positions` | |
| `get_trading_mode` | `GET bot-engine /api/v1/trading-mode` | Verified paper mode, execution flag |
| `create_order_intent(symbol, strategy, idempotency_key=None)` | `POST bot-engine /api/v1/order-intents` | **Proposal only**: executes nothing; a human must approve it. Sends `Idempotency-Key` (generated if omitted, and returned so the agent can retry with it) |
| `get_order_intent(intent_id)` | `GET bot-engine /api/v1/order-intents/{id}` | `intent_id` must be a UUID |
| `list_order_intents(status=None, limit=50)` | `GET bot-engine /api/v1/order-intents` | `status` from the schema enum, `limit` 1-200 |

**The agent can propose, never execute.** `create_order_intent` creates an order intent
that bot-engine sizes and risk-checks; a human approves it (or it expires) before anything
is submitted, on Alpaca paper only. See [ORDER_INTENTS.md](../docs/runbooks/ORDER_INTENTS.md).
There are no approve, reject, cancel, kill-switch or direct order tools. The gateway can
only reach the endpoints in `ALLOWED_ENDPOINTS` ([upstream.py](src/agent_gateway/upstream.py)).
Its service token holds only `market:read`, `portfolio:read` and `intent:create`, **never
`intent:approve`** (ADR-003): bot-engine refuses to issue or load an agent token with
`intent:approve`, and answers 403 on every other write route even if this code changed.

## Safety properties

- **Input validation.** Symbols, strategy names, idempotency keys (`^[A-Za-z0-9._:-]{8,128}$`), intent IDs (UUID) and statuses are validated before any request.
- **Sanitized errors.** Raw broker errors and unexpected fields never reach the agent. Errors read `<code>: <fixed message> (error=<service error code>, request_id=<id>)`; for example `conflict: the kill switch is engaged; no order intent was created (error=kill_switch_engaged, request_id=mcp-agent-3f9c...)`. The service's free-text `message`/`detail` is not forwarded (except signal-service validation messages).
- **No redirects.** The gateway never follows redirects. Plain `http` is allowed only to `localhost`, so the token never leaves the host in clear text.
- **Rate limit.** Calls are limited per agent (`RATE_LIMIT_PER_MINUTE`, default 30).
- **Audit.** Each tool call writes one JSON line to **stderr**, plus `AUDIT_LOG_FILE` if set. The line holds the actor, tool, arguments, outcome, error code and duration, and never the token. stdout is reserved for the MCP protocol.

## Setup

1. Start signal-service and bot-engine (see the root README).
2. Issue a gateway token and add it to the shared table, following [SERVICE_TOKENS.md](../docs/runbooks/SERVICE_TOKENS.md). Use type `agent` and scopes `market:read`, `portfolio:read` and `intent:create`. Never give it `intent:approve`, `orders:place-legacy` or `admin:*`. A token issued before BL-26 (read scopes only) still works for the read tools; `create_order_intent` then fails with `forbidden: ... (requires intent:create)`.
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
