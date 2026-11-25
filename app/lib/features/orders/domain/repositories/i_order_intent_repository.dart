import 'package:dartz/dartz.dart';

import '../../../../core/errors/failures.dart';
import '../entities/order_intent.dart';

/// A live change relevant to intents, from the bot-engine WebSocket.
class IntentLiveUpdate {
  /// The intent carried by `intent.updated`; null for `order.filled`.
  final OrderIntent? intent;

  const IntentLiveUpdate(this.intent);

  /// `order.filled`: the list should be reloaded.
  bool get isOrderFilled => intent == null;
}

abstract class IOrderIntentRepository {
  Future<Either<Failure, List<OrderIntent>>> list({
    IntentStatus? status,
    int limit = 50,
  });

  Future<Either<Failure, OrderIntent>> get(String id);

  Future<Either<Failure, OrderIntent>> create({
    required String symbol,
    required String strategy,
    required String idempotencyKey,
  });

  Future<Either<Failure, IntentActionResult>> approve(String id, {String? reason});

  Future<Either<Failure, OrderIntent>> reject(String id, {required String reason});

  Future<Either<Failure, OrderIntent>> cancel(String id);

  /// `intent.updated` and `order.filled` events (v1 only; others ignored).
  Stream<IntentLiveUpdate> watchUpdates();
}
