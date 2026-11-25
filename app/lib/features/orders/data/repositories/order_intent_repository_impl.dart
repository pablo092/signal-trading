import 'dart:async';

import 'package:dartz/dartz.dart';

import '../../../../core/errors/failures.dart';
import '../../../../core/network/engine_http.dart';
import '../../../signals/data/datasources/signal_remote_data_source.dart';
import '../../../signals/data/datasources/ws_events.dart';
import '../../domain/entities/order_intent.dart';
import '../../domain/repositories/i_order_intent_repository.dart';
import '../datasources/order_intent_remote_data_source.dart';

class OrderIntentRepositoryImpl implements IOrderIntentRepository {
  final OrderIntentRemoteDataSource _remote;
  final SignalRemoteDataSource _ws;

  const OrderIntentRepositoryImpl(this._remote, this._ws);

  Future<Either<Failure, T>> _run<T>(Future<T> Function() call) async {
    try {
      return Right(await call());
    } on ApiException catch (e) {
      return Left(e.failure);
    } on TimeoutException {
      return Left(const TimeoutFailure());
    } on FormatException catch (e) {
      return Left(NetworkFailure('Unexpected response: ${e.message}'));
    } on Exception catch (e) {
      return Left(NetworkFailure(e.toString()));
    } on TypeError {
      return Left(const NetworkFailure('Unexpected response shape'));
    }
  }

  @override
  Future<Either<Failure, List<OrderIntent>>> list({
    IntentStatus? status,
    int limit = 50,
  }) =>
      _run(() => _remote.list(status: status, limit: limit));

  @override
  Future<Either<Failure, OrderIntent>> get(String id) =>
      _run(() => _remote.get(id));

  @override
  Future<Either<Failure, OrderIntent>> create({
    required String symbol,
    required String strategy,
    required String idempotencyKey,
  }) =>
      _run(() => _remote.create(
            symbol: symbol,
            strategy: strategy,
            idempotencyKey: idempotencyKey,
          ));

  @override
  Future<Either<Failure, IntentActionResult>> approve(
    String id, {
    String? reason,
  }) =>
      _run(() => _remote.approve(id, reason: reason));

  @override
  Future<Either<Failure, OrderIntent>> reject(
    String id, {
    required String reason,
  }) =>
      _run(() => _remote.reject(id, reason: reason));

  @override
  Future<Either<Failure, OrderIntent>> cancel(String id) =>
      _run(() => _remote.cancel(id));

  @override
  Stream<IntentLiveUpdate> watchUpdates() => _ws
      .watchEvents()
      .map<IntentLiveUpdate?>((e) {
        if (e.type == wsTypeOrderFilled) return const IntentLiveUpdate(null);
        final intent = e.intent;
        return intent == null ? null : IntentLiveUpdate(intent);
      })
      .where((u) => u != null)
      .cast<IntentLiveUpdate>();
}
