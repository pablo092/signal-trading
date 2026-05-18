import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:dartz/dartz.dart';

import 'package:trading_bot/core/errors/failures.dart';
import 'package:trading_bot/features/signals/domain/entities/signal_entity.dart';
import 'package:trading_bot/features/signals/domain/repositories/i_signal_repository.dart';
import 'package:trading_bot/features/signals/domain/usecases/signal_usecases.dart';

class MockSignalRepository extends Mock implements ISignalRepository {}

void main() {
  late MockSignalRepository repo;
  late GetSignalUseCase getSignal;
  late WatchSignalsUseCase watchSignals;

  setUp(() {
    repo = MockSignalRepository();
    getSignal = GetSignalUseCase(repo);
    watchSignals = WatchSignalsUseCase(repo);
  });

  final tSignal = SignalEntity(
    id: 'abc-123',
    symbol: 'AAPL',
    direction: SignalDirection.buy,
    strength: SignalStrength.strong,
    price: 150.25,
    currency: 'USD',
    confidence: 0.85,
    reason: 'RSI oversold',
    strategyName: 'RSIStrategy(period=14)',
    isActionable: true,
    generatedAt: DateTime(2026, 5, 18),
  );

  group('SignalEntity', () {
    test('confidencePercent formats correctly', () {
      expect(tSignal.confidencePercent, '85.0%');
    });

    test('fromJson round-trips correctly', () {
      final json = {
        'id': 'abc-123',
        'symbol': 'AAPL',
        'direction': 'BUY',
        'strength': 'STRONG',
        'price': 150.25,
        'confidence': 0.85,
        'reason': 'RSI oversold',
        'strategy_name': 'RSIStrategy(period=14)',
        'is_actionable': true,
        'generated_at': '2026-05-18T00:00:00.000',
      };
      final entity = SignalEntity.fromJson(json);
      expect(entity.symbol, 'AAPL');
      expect(entity.direction, SignalDirection.buy);
      expect(entity.confidence, 0.85);
    });
  });

  group('GetSignalUseCase', () {
    test('returns signal on success', () async {
      when(() => repo.getSignal('AAPL', 'rsi'))
          .thenAnswer((_) async => Right(tSignal));

      final result = await getSignal(const GetSignalParams(symbol: 'AAPL', strategy: 'rsi'));
      expect(result, Right(tSignal));
    });

    test('returns failure on network error', () async {
      when(() => repo.getSignal('AAPL', 'rsi'))
          .thenAnswer((_) async => Left(NetworkFailure('timeout')));

      final result = await getSignal(const GetSignalParams(symbol: 'AAPL', strategy: 'rsi'));
      expect(result.isLeft(), true);
    });
  });

  group('WatchSignalsUseCase', () {
    test('returns signal stream', () {
      when(() => repo.watchSignals())
          .thenAnswer((_) => Stream.fromIterable([tSignal]));

      expect(watchSignals(), emits(tSignal));
    });
  });
}
