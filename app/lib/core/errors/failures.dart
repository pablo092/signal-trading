import 'dart:convert';

/// Base failure type for the Either error channel.
sealed class Failure {
  const Failure();
  String get message;
}

class TimeoutFailure extends Failure {
  const TimeoutFailure();
  @override
  String get message => 'The request timed out.';
}

class NetworkFailure extends Failure {
  @override
  final String message;
  const NetworkFailure(this.message);
}

class ServerFailure extends Failure {
  final int statusCode;
  @override
  final String message;
  const ServerFailure({required this.statusCode, required this.message});
}

class SignalNotAvailableFailure extends Failure {
  @override
  final String message;
  const SignalNotAvailableFailure(this.message);
}

class CacheFailure extends Failure {
  @override
  final String message;
  const CacheFailure(this.message);
}

/// A non-2xx answer from bot-engine, decoded from the standard error envelope
/// `{"error", "detail" | "message", "request_id"}` (contracts/schemas/error.schema.json).
class ApiFailure extends Failure {
  /// HTTP status; 0 when no request was sent (e.g. no engine token).
  final int statusCode;

  /// Machine-readable code (`error`), e.g. `intent_expired`.
  final String code;

  @override
  final String message;

  /// Correlation ID from the body or the `X-Request-ID` header, if any.
  final String? requestId;

  /// Scope the token lacks, on `insufficient_scope`.
  final String? requiredScope;

  const ApiFailure({
    required this.statusCode,
    required this.code,
    required this.message,
    this.requestId,
    this.requiredScope,
  });

  /// Decodes an error response. Never throws: a body that is not an envelope
  /// yields `http_<status>` and a generic message.
  factory ApiFailure.fromResponse(
    int statusCode,
    String body, {
    Map<String, String> headers = const {},
  }) {
    Map<String, dynamic>? json;
    try {
      final decoded = jsonDecode(body);
      if (decoded is Map<String, dynamic>) json = decoded;
    } catch (_) {}
    String? str(String key) {
      final v = json?[key];
      return v is String && v.isNotEmpty ? v : null;
    }

    return ApiFailure(
      statusCode: statusCode,
      code: str('error') ?? 'http_$statusCode',
      message: str('message') ?? str('detail') ?? 'Request failed (HTTP $statusCode)',
      requestId: str('request_id') ?? headers['x-request-id'],
      requiredScope: str('required_scope'),
    );
  }

  /// "code · request <id>", for support and logs.
  String get reference =>
      requestId == null ? code : '$code · request $requestId';
}
