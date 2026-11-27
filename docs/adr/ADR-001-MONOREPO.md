# ADR-001 — Monorepo Strategy

| Field | Value |
|---|---|
| Status | **Accepted** (2026-11-27; implemented in M1–M2) |
| Date | 2026-11-24 |
| Deciders | Repository owner |
| Related | TD-09, TD-15, ADR-002 |

## Context
The repository already holds three deployables (`signal-service/` Python, `bot-engine/` Go, `app/` Flutter) plus shared infra (`infra/`, root `docker-compose.yml`, `Makefile`, `.github/workflows/`). One developer maintains it. A fourth deployable, the MCP agent gateway, is planned.

## Problem
Should the project stay a monorepo as the Agent Platform is added, or split per service?

## Options considered
1. **Keep the monorepo** and add `agent-gateway/` as a sibling directory.
2. **Polyrepo**: one repository per service plus an infra repository.
3. **Hybrid**: backend monorepo with the Flutter app in a separate repository.

## Decision
Option 1: keep the monorepo, with these conventions:
- One top-level directory per deployable. Each owns its Dockerfile, dependency manifest, and tests.
- Cross-service contracts live in a new `contracts/` directory (OpenAPI for REST, JSON Schema/AsyncAPI for WS events). Each service tests against them (TD-12, TD-23).
- One CI workflow per service with **correct** path filters, plus a contracts job triggered by any `contracts/**` change.
- The root `docker-compose.yml` is the single local entrypoint. Per-service compose files are removed (TD-15).

## Rationale
- A single maintainer gets atomic cross-service changes, which matters because contracts already drifted (TD-12).
- Existing tooling (Makefile, root compose, CI layout) already assumes a monorepo.
- Splitting would multiply CI and secrets configuration while CI is not yet functional (TD-09).

## Consequences
- Path filters must be maintained carefully; one is already wrong (TD-09).
- Clone size grows with Flutter assets. This is acceptable at the current size (103 tracked files).
- Release versioning is per directory (tags such as `signal-service/v0.2.0`).

## Security implications
- One repository means one place where secret-handling rules apply. `.gitignore` must be fixed (`!.env.example`, SEC-14).
- CI deploy jobs for one service must not get credentials for another. Use per-job GitHub environments.
- Branch protection on `main` becomes the single control point for all services.

## Alternatives rejected
- **Polyrepo:** higher coordination cost and contract drift, with no team-scaling benefit for one maintainer.
- **Hybrid:** the app consumes contracts from both backends, so splitting it out reintroduces the drift problem.

## Open questions
- Should `contracts/` code-generate clients (Go/Dart/Python), or only validate in tests?
