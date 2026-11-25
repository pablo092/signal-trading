#!/usr/bin/env bash
# Build the Flutter web app on Vercel (invoked from the repo-root vercel.json).
#
# Vercel has no Flutter preinstalled, so this script fetches a pinned Flutter SDK
# and runs `flutter build web`. Output: app/build/web (vercel.json outputDirectory).
#
# Optional Vercel environment variables (baked into the bundle at build time,
# so they must NOT contain secrets):
#   SIGNAL_SERVICE_URL   e.g. https://signal-service.example.com
#   BOT_ENGINE_WS_URL    e.g. wss://bot-engine.example.com/ws
#   FLUTTER_VERSION      overrides the pinned SDK tag
set -euo pipefail
cd "$(dirname "$0")/.."   # run from app/ whatever the caller cwd

FLUTTER_VERSION="${FLUTTER_VERSION:-3.41.9}"   # matches app/.metadata
FLUTTER_DIR="${FLUTTER_DIR:-$HOME/flutter-sdk}"

if [ ! -x "$FLUTTER_DIR/bin/flutter" ]; then
  echo "Fetching Flutter $FLUTTER_VERSION..."
  git clone --depth 1 --branch "$FLUTTER_VERSION" \
    https://github.com/flutter/flutter.git "$FLUTTER_DIR"
fi
export PATH="$FLUTTER_DIR/bin:$PATH"

# The Flutter tool needs unzip to unpack the Dart SDK.
if ! command -v unzip >/dev/null 2>&1; then
  dnf install -y unzip >/dev/null 2>&1 || yum install -y unzip >/dev/null 2>&1 || true
fi

flutter config --no-analytics --enable-web >/dev/null
flutter --version

defines=()
[ -n "${SIGNAL_SERVICE_URL:-}" ] && defines+=("--dart-define=SIGNAL_SERVICE_URL=$SIGNAL_SERVICE_URL")
[ -n "${BOT_ENGINE_WS_URL:-}" ] && defines+=("--dart-define=BOT_ENGINE_WS_URL=$BOT_ENGINE_WS_URL")

flutter pub get
flutter build web --release ${defines[@]+"${defines[@]}"}
