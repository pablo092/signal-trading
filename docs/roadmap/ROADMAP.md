# Roadmap — Signal Trading Agent

The milestones are sequential: each one's exit criteria gate the next. Backlog item IDs refer to [IMPLEMENTATION_BACKLOG.md](IMPLEMENTATION_BACKLOG.md).

| Milestone | Goal | Execution capability | Key items | Status |
|---|---|---|---|---|
| **M0** | Technical audit and architecture foundation | none | This document set | **Complete** (PR #2); ADR-001…006 accepted |
| **M1** | Safe read-only foundation; agents can query and analyze the market | **None reachable** (order route disabled, auth on) | BL-01…BL-13 | **Done** (PRs #3–#5, #10, #11) |
| **M2** | Persistence, observability, safety primitives (kill switch, BrokerPort v2) | Unchanged (disabled) | BL-07, BL-09, BL-14…BL-20 | **Done** (PRs #12–#16); Dart, `-race` and Postgres tests pass (BL-36) |
| **M3** | Order intents with mandatory human approval, paper only | Paper, human-approved (still off on Fly: `ORDER_EXECUTION_ENABLED=false`) | BL-21…BL-26 | **Done** (PR #17, on Fly); `-race` and K8s manifest validation pass (BL-36) |
| **M4** | Paper operations: durable persistence (Supabase), metrics and alerts, legacy route removed, track-record rules (ADR-006) | Off on Fly (`ORDER_EXECUTION_ENABLED=false`) until the owner starts phase 1 | BL-31…BL-35 | **Done** (PRs #18–#21, on Fly with Supabase); ADR-006 entry gate passed 2026-11-26 |
| **Paper phase 1** | Run the ADR-006 track record: ≥ 20 trading days and ≥ 30 fills, weekly review, stop rules | Paper, every order approved by the owner | BL-37 | **In progress** since 2026-11-27 (execution on in `bot-engine/fly.toml`) |
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
