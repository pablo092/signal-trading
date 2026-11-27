# Trading Bot app (Flutter)

Operator UI for signal-trading: market signals, portfolio, order intents and their human
approval. One codebase serves two builds:

| Build | Where | Engine access |
|---|---|---|
| **Public web demo** | Vercel (`https://signal-trading-demo.vercel.app`) | none: signals and a read-only portfolio summary go through Vercel functions that hold a read-only token server-side |
| **Local build** | your machine | direct, with a bearer token passed at build time: portfolio, propose intents, approve/reject (human token) |

A public build never carries a token (ADR-003); see [DEMO.md](../docs/runbooks/DEMO.md) for the
full demo.

## Structure

```text
lib/
  core/        config (--dart-define), errors, engine HTTP client
  features/
    signals/   generate and watch signals (BLoC, clean layers)
    portfolio/ equity and positions
    orders/    order intents: propose, list, approve/reject (BL-25)
    dashboard/ main page and tabs
  shared/      theme and widgets
test/          BLoC, domain, engine API, widget and contract tests
```

## Configuration

All settings are `--dart-define` values, compiled into the bundle and therefore public.

| Define | Default | Meaning |
|---|---|---|
| `SIGNAL_SERVICE_URL` | `http://localhost:8000` | signal-service base URL; `same-origin` = the hosting site's `/api` (Vercel) |
| `BOT_ENGINE_URL` | `http://localhost:8080` | bot-engine REST base URL |
| `BOT_ENGINE_TOKEN` | empty | engine bearer token, **local builds only**; empty disables every engine feature |
| `BOT_ENGINE_WS_URL` | `ws://localhost:8080/ws` | live feed; empty disables it |
| `BOT_ENGINE_WS_TOKEN` | empty | token for the `/ws` upgrade (`portfolio:read`), local builds only |

## Run locally

Flutter **3.41.9** (pinned to match the Vercel build). If it is not on your `PATH`, call it by
its full path (e.g. `~/tools/flutter/bin/flutter`).

Against a local stack (`make dev` at the repo root):

```sh
flutter pub get
flutter run -d chrome --web-port 5000 \
  --dart-define=BOT_ENGINE_TOKEN=<token> --dart-define=BOT_ENGINE_WS_TOKEN=<token>
```

Against the deployed engine on Fly, follow [DEMO.md](../docs/runbooks/DEMO.md): it uses port
5000 (the only browser origin the engine and the Vercel proxy accept) and reads the token
without leaving it in the shell history.

What the token allows depends on its scopes ([SERVICE_TOKENS.md](../docs/runbooks/SERVICE_TOKENS.md)):
`portfolio:read` for the Portfolio tab, `intent:create` for *Propose trade*, and a **human**
token with `intent:approve` for Approve/Reject. Approval also needs `ORDER_EXECUTION_ENABLED=true`
on the engine; otherwise it answers `execution_disabled`
([ORDER_INTENTS.md](../docs/runbooks/ORDER_INTENTS.md)).

## Tests

```sh
flutter analyze
flutter test
```

Contract tests check the app's models against the JSON Schemas in [contracts/](../contracts/).

## Deploy to Vercel (web)

1. Import the repo in Vercel and leave **Root Directory empty** and the framework preset on
   "Other". The root [vercel.json](../vercel.json) builds only this app, and
   [.vercelignore](../.vercelignore) hides the backends so Vercel does not detect the Python
   service (`PYTHON_ENTRYPOINT_NOT_FOUND`).
2. [scripts/vercel-build.sh](scripts/vercel-build.sh) installs the pinned Flutter SDK and runs
   `flutter build web` with `SIGNAL_SERVICE_URL=same-origin` and the live feed off; output is
   `app/build/web`.
3. The functions in [api/](../api/) need the Vercel env var `SIGNAL_SERVICE_TOKEN` (the
   read-only `vercel-demo` token). Never set `BOT_ENGINE_TOKEN` on Vercel.

Pushing to `main` redeploys the site.
