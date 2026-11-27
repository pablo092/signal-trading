import 'browser_notifier_stub.dart'
    if (dart.library.js_interop) 'browser_notifier_web.dart' as impl;

/// Operating-system notifications through the browser's Notification API.
/// Only the web build shows them; elsewhere every call is a no-op.
abstract final class BrowserNotifier {
  /// The browser can show notifications at all.
  static bool get supported => impl.supported();

  /// "granted", "denied" or "default" (not asked yet); "unsupported" off the web.
  static String get permission => impl.permission();

  /// Asks for permission. Browsers only allow this from a user gesture.
  static Future<String> requestPermission() => impl.requestPermission();

  /// Shows a notification if permission was granted. [tag] replaces an
  /// earlier notification with the same tag instead of stacking them.
  static void show(String title, {required String body, String? tag}) =>
      impl.show(title, body: body, tag: tag);
}
