import 'dart:math';

/// Format bot-engine accepts for the `Idempotency-Key` header.
final RegExp idempotencyKeyPattern = RegExp(r'^[A-Za-z0-9._:-]{8,128}$');

/// A new random `Idempotency-Key`: `app-` plus 32 hex chars (128 bits from
/// [Random.secure]). Pass [random] only in tests.
String newIdempotencyKey([Random? random]) {
  final r = random ?? Random.secure();
  final buffer = StringBuffer('app-');
  for (var i = 0; i < 16; i++) {
    buffer.write(r.nextInt(256).toRadixString(16).padLeft(2, '0'));
  }
  return buffer.toString();
}
