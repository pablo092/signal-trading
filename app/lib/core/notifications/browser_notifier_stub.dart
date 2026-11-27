bool supported() => false;

String permission() => 'unsupported';

Future<String> requestPermission() async => 'unsupported';

void show(String title, {required String body, String? tag}) {}
