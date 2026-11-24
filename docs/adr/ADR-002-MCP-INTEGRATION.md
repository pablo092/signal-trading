# ADR-002 — MCP Integration

| Field | Value |
|---|---|
| Status | **Proposed** |
| Date | 2026-11-24 |
| Deciders | Repository owner |
| Related | ADR-003, ADR-005, TARGET_ARCHITECTURE §4 |

## Context
The goal of the Agent Platform is to let AI agents (Claude, Codex, IDE assistants) query and analyze the market through this system. The Model Context Protocol (MCP) is the integration standard. It supports two transports: **STDIO** (local subprocess) and **Streamable HTTP** (remote). The current system exposes REST APIs with no authentication (SEC-01, SEC-09), and its order endpoint is unsafe for automated callers (SEC-01…SEC-05).

## Problem
1. Where does MCP integration live?
2. What does it expose?
3. Which transports should it support, and when?

## Options considered

| # | Option | Summary |
|---|---|---|
| A | **Separate thin `agent-gateway/` process** calling existing REST APIs | New deployable with no domain logic |
| B | Embed MCP endpoints inside signal-service | Reuse the Python process |
| C | Embed MCP inside bot-engine | Gives the agent direct proximity to execution |
| D | No MCP; agents call REST directly | Uses OpenAPI-based tool calling |

Implementation language for A: Python (official `mcp` SDK, same stack as signal-service), TypeScript (reference SDK), or Go (community/official Go SDK).

## Decision (proposed)
**Option A in Python**, with this scope:
- **M1 transport: STDIO only.** The gateway runs as a local subprocess of the MCP client and calls services on `localhost` with a service token.
- **Later: Streamable HTTP**, only after ADR-003 OAuth 2.1/OIDC is implemented. No remote transport without authentication.
- **M1 tools: read-only** (`list_strategies`, `generate_signal`, `get_portfolio_summary`, `get_positions`, `get_trading_mode`). MCP *resources* may expose strategy descriptions and risk-rule configuration read-only.
- **No order tools** until ADR-005 is implemented. The first write tool will be `create_order_intent`, never a direct "place order".
- The gateway holds **no trading logic**. It validates input shape, maps tools to REST calls, attaches agent identity, and writes an audit record per call.

## Rationale
- Option A keeps a hard process boundary between agents and execution. The gateway can be given network access to read endpoints only.
- Python matches signal-service tooling, so one less toolchain to maintain. Gateway logic is I/O glue, where Python's SDK maturity outweighs Go's performance.
- STDIO first avoids building network authentication before the read-only value is proven, and is the transport local clients (Codex, Claude Code/Desktop) support natively.

## Consequences
- One new deployable and CI job.
- Tool schemas become a public contract and must be versioned (ties to ADR-001 `contracts/`).
- Read-only tools still consume Alpaca data quota. Caching (TD-13) and per-agent rate limits are required.

## Security implications
- Prompt injection is possible through market data or tool output, for example ticker news reaching the model. Read-only scope bounds the blast radius: the worst case in M1 is quota use and misleading analysis, not orders.
- The gateway's service token must carry read scopes only. bot-engine must reject it on any write route (ADR-003).
- Tool outputs must not include secrets, raw broker errors, or account identifiers beyond what is needed (TD-19).

## Alternatives rejected
- **B (embed in signal-service):** couples the agent protocol lifecycle to the signal engine and makes it harder to later front bot-engine reads with the same identity layer.
- **C (embed in bot-engine):** puts agents inside the execution authority's process, which is the opposite of the isolation goal.
- **D (REST only):** loses MCP's client interoperability and tool/resource semantics, and still needs the same auth work.

## Open questions
- Which MCP clients must be supported first (Codex CLI, Claude Code, Claude Desktop)? This affects packaging (`uvx` vs Docker).
- Should `generate_signal` results be persisted to Postgres for agent-history queries (M2)?
