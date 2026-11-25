/// Build-time configuration (`--dart-define`). Values are public: never secrets.
class AppConfig {
  AppConfig._();

  static const String _apiUrl = String.fromEnvironment(
    'SIGNAL_SERVICE_URL',
    defaultValue: 'http://localhost:8000',
  );

  /// Empty disables the live WebSocket feed (web demo, until BL-15).
  static const String wsUrl = String.fromEnvironment(
    'BOT_ENGINE_WS_URL',
    defaultValue: 'ws://localhost:8080/ws',
  );

  /// Token for the /ws upgrade (portfolio:read). Local development only: a
  /// value set here is visible in the bundle, so never set it on Vercel.
  static const String wsToken = String.fromEnvironment('BOT_ENGINE_WS_TOKEN');

  /// bot-engine REST base URL (portfolio, order intents).
  static const String engineUrl = String.fromEnvironment(
    'BOT_ENGINE_URL',
    defaultValue: 'http://localhost:8080',
  );

  /// Bearer token for bot-engine REST calls (portfolio:read, intent:create,
  /// and intent:approve for a human token). Local development only: a value
  /// set here is visible in the bundle, so never set it on Vercel. Empty
  /// disables every engine feature (the app then never calls bot-engine).
  static const String engineToken = String.fromEnvironment('BOT_ENGINE_TOKEN');

  /// 'same-origin' = the hosting site (Vercel demo proxy under /api).
  static String get apiBaseUrl =>
      _apiUrl == 'same-origin' ? Uri.base.origin : _apiUrl;

  static bool get isWebDemo => _apiUrl == 'same-origin';

  static bool get liveFeedEnabled => wsUrl.isNotEmpty;

  /// True when the app may call bot-engine directly. Never on the public web
  /// demo, even if a token was baked in by mistake.
  static bool get engineEnabled => engineToken.isNotEmpty && !isWebDemo;

  /// Portfolio reads: direct with a token, or through the read-only demo
  /// proxy (which holds its token server-side) on the web demo.
  static bool get portfolioAvailable => engineEnabled || isWebDemo;
}
