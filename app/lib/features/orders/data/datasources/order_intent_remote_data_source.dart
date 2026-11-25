import '../../../../core/network/engine_http.dart';
import '../../../../core/network/idempotency.dart';
import '../../domain/entities/order_intent.dart';

/// bot-engine /api/v1/order-intents (docs/runbooks/ORDER_INTENTS.md).
class OrderIntentRemoteDataSource {
  final EngineHttp _engine;

  const OrderIntentRemoteDataSource(this._engine);

  static const String _path = '/api/v1/order-intents';

  /// GET /order-intents?status=&limit= (newest first).
  Future<List<OrderIntent>> list({IntentStatus? status, int limit = 50}) async {
    final response = await _engine.get(_path, {
      if (status != null) 'status': status.wire,
      'limit': '$limit',
    });
    final json = EngineHttp.decode(response) as Map<String, dynamic>;
    return OrderIntentList.fromJson(json).intents;
  }

  /// GET /order-intents/{id}
  Future<OrderIntent> get(String id) async {
    final response = await _engine.get('$_path/${Uri.encodeComponent(id)}');
    return OrderIntent.fromJson(
        EngineHttp.decode(response) as Map<String, dynamic>);
  }

  /// POST /order-intents (201; a replay answers with the original status).
  /// The engine fetches the signal and sizes the order itself.
  Future<OrderIntent> create({
    required String symbol,
    required String strategy,
    String? idempotencyKey,
  }) async {
    final response = await _engine.post(
      _path,
      {'symbol': symbol, 'strategy': strategy},
      headers: {'Idempotency-Key': idempotencyKey ?? newIdempotencyKey()},
    );
    return OrderIntent.fromJson(
        EngineHttp.decode(response, ok: const {200, 201})
            as Map<String, dynamic>);
  }

  /// POST /order-intents/{id}/approve (human token, intent:approve).
  /// 200 = outcome known (submitted, or rejected with a code);
  /// 202 = broker outcome unknown, the reconciler resolves it.
  Future<IntentActionResult> approve(String id, {String? reason}) async {
    final response = await _engine.post(
      '$_path/${Uri.encodeComponent(id)}/approve',
      {if (reason != null && reason.isNotEmpty) 'reason': reason},
    );
    final json =
        EngineHttp.decode(response, ok: const {200, 202}) as Map<String, dynamic>;
    return IntentActionResult(
      intent: OrderIntent.fromJson(json),
      outcomeUnknown: response.statusCode == 202,
    );
  }

  /// POST /order-intents/{id}/reject (human token; reason 1-200 chars).
  Future<OrderIntent> reject(String id, {required String reason}) async {
    final response = await _engine.post(
      '$_path/${Uri.encodeComponent(id)}/reject',
      {'reason': reason},
    );
    return OrderIntent.fromJson(
        EngineHttp.decode(response) as Map<String, dynamic>);
  }

  /// POST /order-intents/{id}/cancel (creator only, while pending_approval).
  Future<OrderIntent> cancel(String id) async {
    final response = await _engine.post(
      '$_path/${Uri.encodeComponent(id)}/cancel',
      const <String, dynamic>{},
    );
    return OrderIntent.fromJson(
        EngineHttp.decode(response) as Map<String, dynamic>);
  }
}
