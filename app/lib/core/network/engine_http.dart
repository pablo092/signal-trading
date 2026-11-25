import 'dart:convert';

import 'package:http/http.dart' as http;

import '../errors/failures.dart';

/// Thrown by engine data sources; carries the decoded error envelope.
class ApiException implements Exception {
  final ApiFailure failure;
  const ApiException(this.failure);
  @override
  String toString() => 'ApiException(${failure.statusCode} ${failure.reference})';
}

/// Failure used when no engine token is configured: no request is sent.
const ApiFailure engineTokenMissing = ApiFailure(
  statusCode: 0,
  code: 'engine_token_missing',
  message: 'Connect a bot-engine token (local builds only) to use this.',
);

/// Shared plumbing for authenticated bot-engine REST calls.
class EngineHttp {
  final http.Client _http;
  final String _baseUrl;
  final String _token;

  static const Duration _timeout = Duration(seconds: 15);

  EngineHttp({
    required http.Client httpClient,
    required String baseUrl,
    required String token,
  })  : _http = httpClient,
        _baseUrl = baseUrl,
        _token = token;

  bool get enabled => _token.isNotEmpty;

  Map<String, String> _headers([Map<String, String> extra = const {}]) => {
        'Authorization': 'Bearer $_token',
        'Accept': 'application/json',
        ...extra,
      };

  Uri uri(String path, [Map<String, String>? query]) {
    final base = Uri.parse('$_baseUrl$path');
    return query == null || query.isEmpty
        ? base
        : base.replace(queryParameters: query);
  }

  void _guard() {
    if (!enabled) throw const ApiException(engineTokenMissing);
  }

  Future<http.Response> get(String path, [Map<String, String>? query]) async {
    _guard();
    return _http.get(uri(path, query), headers: _headers()).timeout(_timeout);
  }

  Future<http.Response> post(
    String path,
    Map<String, dynamic> body, {
    Map<String, String> headers = const {},
  }) async {
    _guard();
    return _http
        .post(
          uri(path),
          headers: _headers({'Content-Type': 'application/json', ...headers}),
          body: jsonEncode(body),
        )
        .timeout(_timeout);
  }

  /// Decodes a 2xx JSON body, or throws [ApiException] with the envelope.
  static dynamic decode(http.Response response, {Set<int> ok = const {200}}) {
    if (!ok.contains(response.statusCode)) {
      throw ApiException(ApiFailure.fromResponse(
        response.statusCode,
        response.body,
        headers: response.headers,
      ));
    }
    return jsonDecode(response.body);
  }
}
