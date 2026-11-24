# Roadmap — Signal Trading Agent

The milestones are sequential: each one's exit criteria gate the next. Backlog item IDs refer to [IMPLEMENTATION_BACKLOG.md](IMPLEMENTATION_BACKLOG.md).

| Milestone | Goal | Execution capability | Key items | Status |
|---|---|---|---|---|
| **M0** | Technical audit and architecture foundation | none | This document set | **Complete, pending owner review** |
| **M1** | Safe read-only foundation; agents can query and analyze the market | **None reachable** (order route disabled, auth on) | BL-01…BL-13 | Planned |
| **M2** | Persistence, observability, safety primitives (kill switch, BrokerPort v2) | Unchanged (disabled) | BL-07, BL-09, BL-14…BL-20 | Planned |
| **M3** | Order intents with mandatory human approval, paper only | Paper, human-approved | BL-21…BL-26 | Planned |
| Later | Async flows, Timescale, remote MCP transport, live trading | Each requires its own ADR | BL-27…BL-30 | Not planned |

## M1 definition of done

1. A quality gate runs and is green (BL-01…BL-04).
2. bot-engine refuses to start on a non-paper host, and order execution is disabled by default (BL-05, BL-06).
3. All service routes require a scoped token (BL-06).
4. An MCP client, via STDIO, can call `list_strategies`, `generate_signal`, `get_portfolio_summary`, `get_positions`, `get_trading_mode` (BL-10).
5. A test proves the agent-gateway token is rejected on every non-read route.
6. README and Compose describe and start what actually exists (BL-11, BL-13).

## Principles carried across milestones
- No new business microservices; the only new process is the MCP gateway.
- The MCP layer holds no trading logic.
- Fail closed. Paper only. Every write is idempotent and audited.
