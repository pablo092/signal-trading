import 'dart:js_interop';
import 'dart:js_interop_unsafe';

@JS('Notification')
extension type _Notification._(JSObject _) implements JSObject {
  external _Notification(String title, _NotificationOptions options);
  external static String get permission;
  external static JSPromise<JSString> requestPermission();
}

extension type _NotificationOptions._(JSObject _) implements JSObject {
  external factory _NotificationOptions({String body, String tag});
}

bool supported() => globalContext.has('Notification');

String permission() => supported() ? _Notification.permission : 'unsupported';

Future<String> requestPermission() async {
  if (!supported()) return 'unsupported';
  final result = await _Notification.requestPermission().toDart;
  return result.toDart;
}

void show(String title, {required String body, String? tag}) {
  if (!supported() || _Notification.permission != 'granted') return;
  _Notification(
    title,
    tag == null
        ? _NotificationOptions(body: body)
        : _NotificationOptions(body: body, tag: tag),
  );
}
