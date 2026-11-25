// BL-25: error-envelope mapping, Idempotency-Key format and engine gating.
import 'dart:math';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:trading_bot/core/errors/failures.dart';
import 'package:trading_bot/core/network/engine_http.dart';
import 'package:trading_bot/core/network/idempotency.dart';
import 'package:trading_bot/features/orders/presentation/bloc/intents_bloc.dart';

void main() {
  group('ApiFailure.fromResponse', () {
    test('reads error, message and request_id', () {
      final f = ApiFailure.fromResponse(
        409,
        '{"error":"intent_expired","message":"intent expired",'
        '"request_id":"req-123"}',
      );
      expect(f.statusCode, 409);
      expect(f.code, 'intent_expired');
      expect(f.message, 'intent expired');
      expect(f.requestId, 'req-123');
      expect(f.requiredScope, isNull);
      expect(f.reference, 'intent_expired · request req-123');
    });

    test('accepts detail (error.schema.json) in place of message', () {
      final f = ApiFailure.fromResponse(
        403,
        '{"error":"execution_disabled","detail":"order execution is disabled"}',
      );
      expect(f.code, 'execution_disabled');
      expect(f.message, 'order execution is disabled');
      expect(f.requestId, isNull);
      expect(f.reference, 'execution_disabled');
    });

    test('falls back to the X-Request-ID header', () {
      final f = ApiFailure.fromResponse(
        429,
        '{"error":"rate_limited","detail":"slow down"}',
        headers: const {'x-request-id': 'hdr-9'},
      );
      expect(f.requestId, 'hdr-9');
    });

    test('a body that is not an envelope never throws', () {
      final f = ApiFailure.fromResponse(
        502,
        '<html>Bad Gateway</html>',
        headers: const {'x-request-id': 'edge-1'},
      );
      expect(f.statusCode, 502);
      expect(f.code, 'http_502');
      expect(f.message, contains('502'));
      expect(f.requestId, 'edge-1');

      expect(ApiFailure.fromResponse(500, '').code, 'http_500');
      expect(ApiFailure.fromResponse(500, '[1,2]').code, 'http_500');
      expect(ApiFailure.fromResponse(500, '{"error":7}').code, 'http_500');
    });
  });

  group('intentFailureMessage', () {
    ApiFailure f(int status, String code) => ApiFailure(
          statusCode: status,
          code: code,
          message: 'raw $code',
          requestId: 'r1',
        );

    test('friendly text for the documented intent errors', () {
      expect(intentFailureMessage(f(403, 'execution_disabled')),
          contains('execution is disabled'));
      expect(intentFailureMessage(f(409, 'intent_not_pending')),
          contains('no longer pending'));
      expect(intentFailureMessage(f(409, 'intent_expired')), contains('expired'));
      expect(intentFailureMessage(f(409, 'kill_switch_engaged')),
          contains('kill switch'));
      expect(intentFailureMessage(f(429, 'rate_limited')),
          contains('Too many'));
      expect(intentFailureMessage(f(429, 'something_else')),
          contains('Too many'));
      expect(intentFailureMessage(f(401, 'unauthorized')),
          contains('BOT_ENGINE_TOKEN'));
    });

    test('names the missing scope', () {
      const scope = ApiFailure(
        statusCode: 403,
        code: 'insufficient_scope',
        message: 'x',
        requiredScope: 'intent:approve',
      );
      expect(intentFailureMessage(scope), contains('intent:approve'));
    });

    test('unknown codes keep the server message', () {
      expect(intentFailureMessage(f(422, 'invalid_request')),
          'raw invalid_request');
    });

    test('timeouts warn that the outcome is unknown', () {
      expect(intentFailureMessage(const TimeoutFailure()),
          contains('outcome is unknown'));
    });
  });

  group('newIdempotencyKey', () {
    test('matches the engine format', () {
      for (var i = 0; i < 200; i++) {
        final key = newIdempotencyKey();
        expect(idempotencyKeyPattern.hasMatch(key), isTrue, reason: key);
        expect(RegExp(r'^[A-Za-z0-9._:-]{8,128}$').hasMatch(key), isTrue);
        expect(key, matches(RegExp(r'^app-[0-9a-f]{32}$')));
      }
    });

    test('keys are unique', () {
      final keys = List.generate(500, (_) => newIdempotencyKey()).toSet();
      expect(keys.length, 500);
    });

    test('is deterministic with a seeded Random (tests only)', () {
      expect(newIdempotencyKey(Random(1)), newIdempotencyKey(Random(1)));
    });
  });

  group('EngineHttp without a token', () {
    test('is disabled and refuses to send', () async {
      final engine = EngineHttp(
        httpClient: _NoNetworkClient(),
        baseUrl: 'http://localhost:8080',
        token: '',
      );
      expect(engine.enabled, isFalse);
      await expectLater(
        engine.get('/api/v1/order-intents'),
        throwsA(isA<ApiException>().having(
            (e) => e.failure.code, 'code', 'engine_token_missing')),
      );
    });
  });
}

/// Fails the test if any request is sent.
class _NoNetworkClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) =>
      throw StateError('no request may be sent without a token');
}
