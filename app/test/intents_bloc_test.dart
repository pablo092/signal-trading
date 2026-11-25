// BL-25: IntentsBloc behaviour (idempotent retries, live updates).
import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:dartz/dartz.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'package:trading_bot/core/errors/failures.dart';
import 'package:trading_bot/features/orders/domain/entities/order_intent.dart';
import 'package:trading_bot/features/orders/domain/repositories/i_order_intent_repository.dart';
import 'package:trading_bot/features/orders/presentation/bloc/intents_bloc.dart';

class MockIntentRepository extends Mock implements IOrderIntentRepository {}

Map<String, dynamic> _exampleJson() =>
    jsonDecode(File('../contracts/examples/order-intent.json').readAsStringSync())
        as Map<String, dynamic>;

void main() {
  late MockIntentRepository repo;

  setUpAll(() => registerFallbackValue(IntentStatus.pendingApproval));

  setUp(() => repo = MockIntentRepository());

  test('a proposal retried after a timeout reuses its Idempotency-Key', () async {
    final intent = OrderIntent.fromJson(_exampleJson());
    final keys = <String>[];
    when(() => repo.create(
          symbol: any(named: 'symbol'),
          strategy: any(named: 'strategy'),
          idempotencyKey: any(named: 'idempotencyKey'),
        )).thenAnswer((inv) async {
      keys.add(inv.namedArguments[#idempotencyKey] as String);
      final Either<Failure, OrderIntent> result =
          keys.length == 1 ? Left(const TimeoutFailure()) : Right(intent);
      return result;
    });
    final bloc = IntentsBloc(repository: repo);

    bloc.add(const IntentProposed(symbol: 'AAPL', strategy: 'rsi'));
    final failed = await bloc.stream
        .firstWhere((s) => !s.proposing && s.notice != null);
    expect(failed.notice!.isError, isTrue);

    bloc.add(const IntentProposed(symbol: 'AAPL', strategy: 'rsi'));
    final ok = await bloc.stream.firstWhere((s) => s.lastProposed != null);
    expect(ok.lastProposed!.id, intent.id);
    expect(ok.pending.map((i) => i.id).toList(), [intent.id]);

    bloc.add(const IntentProposed(symbol: 'AAPL', strategy: 'rsi'));
    await bloc.stream.firstWhere((s) => !s.proposing && s.notice!.seq == 3);

    expect(keys.length, 3);
    expect(keys[1], keys[0]); // replay of the unknown outcome
    expect(keys[2], isNot(keys[1])); // a new proposal after a success
    await bloc.close();
  });

  test('intent.updated moves an intent out of the pending list', () async {
    final controller = StreamController<IntentLiveUpdate>();
    when(() => repo.watchUpdates()).thenAnswer((_) => controller.stream);
    final pending = OrderIntent.fromJson(_exampleJson());
    final rejected = OrderIntent.fromJson({
      ..._exampleJson(),
      'status': 'rejected',
      'rejection_code': 'rejected_by_approver',
      'updated_at': '2026-11-25T14:31:00Z',
    });
    final bloc = IntentsBloc(repository: repo);
    bloc.add(const IntentsWatchStarted());
    await Future<void>.delayed(Duration.zero);

    controller.add(IntentLiveUpdate(pending));
    final s1 = await bloc.stream.firstWhere((s) => s.pending.isNotEmpty);
    expect(s1.pending.single.id, pending.id);

    controller.add(IntentLiveUpdate(rejected));
    final s2 = await bloc.stream.firstWhere((s) => s.pending.isEmpty);
    expect(s2.recent.single.status, IntentStatus.rejected);

    // A stale update (older updated_at) does not overwrite the newer one.
    controller.add(IntentLiveUpdate(pending));
    await Future<void>.delayed(const Duration(milliseconds: 10));
    expect(bloc.state.recent.single.status, IntentStatus.rejected);

    await controller.close();
    await bloc.close();
  });
}
