# trading_bot

A new Flutter project.

## Getting Started

This project is a starting point for a Flutter application.

A few resources to get you started if this is your first Flutter project:

- [Learn Flutter](https://docs.flutter.dev/get-started/learn-flutter)
- [Write your first Flutter app](https://docs.flutter.dev/get-started/codelab)
- [Flutter learning resources](https://docs.flutter.dev/reference/learning-resources)

For help getting started with Flutter development, view the
[online documentation](https://docs.flutter.dev/), which offers tutorials,
samples, guidance on mobile development, and a full API reference.

## Deploy to Vercel (web)

1. Import the repo in Vercel and leave **Root Directory empty** and the framework preset
   on "Other". The root [vercel.json](../vercel.json) builds only this app, and
   [.vercelignore](../.vercelignore) hides the backends so Vercel does not detect the
   Python service (`PYTHON_ENTRYPOINT_NOT_FOUND`).
2. [scripts/vercel-build.sh](scripts/vercel-build.sh) installs a pinned Flutter SDK
   (3.41.9) and runs `flutter build web`; output is `app/build/web`.
3. Optional environment variables (baked into the public bundle, so never put tokens here):
   - `SIGNAL_SERVICE_URL`: public https URL of signal-service (default `http://localhost:8000`)
   - `BOT_ENGINE_WS_URL`: public `wss://` URL of bot-engine `/ws` (default `ws://localhost:8080/ws`)

Limitations today: signal-service and bot-engine require a service token (ADR-003),
and a browser app cannot hold one safely, so live data needs the user-auth work
planned in BL-15. The deployed app renders its UI; data calls fail until then.

## Portfolio, order intents and approvals (local only, BL-25)

These features call bot-engine directly with a bearer token, so they are enabled only in
local builds. A public web build must never hold a token: without one the Portfolio,
Orders and Approvals tabs show a "connect a token (local only)" state and the app never
calls bot-engine (the web demo keeps its read-only `/api/v1/portfolio/summary` proxy and
the signal flow is unchanged).

```sh
flutter run -d chrome   --dart-define=BOT_ENGINE_URL=http://localhost:8080   --dart-define=BOT_ENGINE_TOKEN=<token>   --dart-define=BOT_ENGINE_WS_TOKEN=<token>
```

- "Propose trade" creates an order intent (`POST /api/v1/order-intents`, scope
  `intent:create`, with an `Idempotency-Key`); the result shows its status and risk checks.
- The Approvals tab lists `pending_approval` intents. Approve / Reject need a **human**
  token with `intent:approve`; approval also needs `ORDER_EXECUTION_ENABLED=true` on the
  engine. See [ORDER_INTENTS.md](../docs/runbooks/ORDER_INTENTS.md).
