# ADR-003 — Agent Security

| Field | Value |
|---|---|
| Status | **Proposed** |
| Date | 2026-11-24 |
| Deciders | Repository owner |
| Related | SEC-01, SEC-05, SEC-06, SEC-09, ADR-002, ADR-005 |

## Context
No component has authentication or authorization today (SECURITY_AUDIT §5). Agents will be a new class of caller. They are non-deterministic and susceptible to prompt injection, and they may loop.

## Problem
Define identity, authentication, authorization, permissions, and audit requirements for agents and for service-to-service calls.

## Options considered
1. **Static service tokens** (opaque, hashed at rest) with scopes, issued per caller.
2. **Signed JWTs** from a local issuer, with scopes and short TTL.
3. **OAuth 2.1 / OIDC** with an external IdP, as the MCP authorization spec requires for remote HTTP transports.
4. **mTLS** between services.

## Decision (proposed)
Phase the controls with the transport:

| Phase | Caller | Mechanism |
|---|---|---|
| M1 (STDIO, local) | agent-gateway → services | Option 1: per-caller static token with scopes, from env/secret file, verified by middleware in both services |
| M1 | app → services | Same mechanism, separate token and scopes |
| Remote HTTP transport (later) | MCP client → agent-gateway | Option 3: OAuth 2.1 with PKCE, per the MCP authorization spec; IdP to be chosen at that time |
| Internal (later, if multi-host) | service ↔ service | Option 2 or 4 |

**Scopes** (initial set):

| Scope | Grants |
|---|---|
| `market:read` | signal-service generate/strategies |
| `portfolio:read` | equity, positions, trading mode |
| `intent:create` | create order intent (M3+) |
| `intent:approve` | approve or reject intents (**humans only**, never agent tokens) |
| `admin:killswitch` | engage or release the kill switch (**humans only**) |

**Mandatory rules:**
- Deny by default. Every route declares its required scope, and unscoped routes fail CI (route-table test).
- Agent tokens can never hold `intent:approve` or `admin:*`. This is enforced at issuance and at verification.
- Every authenticated request is audited: actor ID, actor type (`agent|human|service`), route, scope, request hash, outcome, correlation ID. Writes go to an append-only `audit_events` table (M2). Until M2, they go to structured logs.
- Per-actor rate limits, stricter for agents.
- WebSocket upgrade requires a token and an origin allowlist (SEC-06).

## Rationale
- Static scoped tokens are the smallest change that closes SEC-01 and SEC-09 for a single-host STDIO setup.
- OAuth is required by MCP only for remote HTTP. Building it before a remote transport exists adds complexity with no risk reduction.
- Splitting `create` from `approve` keeps a human decision in the loop regardless of agent behavior.

## Consequences
- Both services get auth middleware and a route-scope table.
- Token issuance and rotation are a manual runbook in M1.
- The Flutter app needs token storage. For the web build, keep it in memory, not `localStorage`.

## Security implications
- Static tokens can leak via env dumps or logs. Mitigate by storing them hashed server-side, redacting them in logs, scoping them minimally, and rotating them.
- Prompt-injected agents are bounded by scope: an M1 agent token cannot trade.
- The audit log must be tamper-evident. Use append-only privileges at the DB role level in M2.

## Alternatives rejected
- **OIDC now:** no remote transport exists yet; high setup cost for a single-user local system.
- **mTLS now:** solves transport identity, not per-agent authorization; heavy certificate management.
- **Network isolation alone:** does not distinguish agent from human callers and gives no audit.

## Open questions
- Is a multi-user deployment ever expected? This changes the IdP choice.
- Where are tokens stored for the Fly.io deployment (Fly secrets vs external vault)?
