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

  /// 'same-origin' = the hosting site (Vercel demo proxy under /api).
  static String get apiBaseUrl =>
      _apiUrl == 'same-origin' ? Uri.base.origin : _apiUrl;

  static bool get isWebDemo => _apiUrl == 'same-origin';

  static bool get liveFeedEnabled => wsUrl.isNotEmpty;
}
