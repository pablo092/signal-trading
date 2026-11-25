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
