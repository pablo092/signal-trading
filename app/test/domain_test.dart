import 'package:dartz/dartz.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'package:trading_bot/core/errors/failures.dart';
import 'package:trading_bot/features/signals/domain/entities/signal_entity.dart';
import 'package:trading_bot/features/signals/domain/repositories/i_signal_repository.dart';
import 'package:trading_bot/features/signals/domain/usecases/signal_usecases.dart';
import 'package:trading_bot/features/portfolio/domain/entities/portfolio_entity.dart';
import 'package:trading_bot/features/orders/domain/entities/order_entity.dart';

class MockSignalRepository extends Mock implements ISignalRepository {}

SignalEntity _makeSignal({
  SignalDirection direction = SignalDirection.buy,
  double confidence = 0.85,
  bool isActionable = true,
}) =>
    SignalEntity(
      id: 'test-id',
      symbol: 'AAPL',
      direction: direction,
      strength: SignalStrength.strong,
      price: 150.0,
      currency: 'USD',
      confidence: confidence,
      reason: 'RSI=22',
      strategyName: 'RSIStrategy(period=14)',
      isActionable: isActionable,
      generatedAt: DateTime(2024, 6, 1, 10, 0),
    );

void main() {
  // ══════════════════════════════════════════════
  // SignalEntity
  // ══════════════════════════════════════════════

  group('SignalEntity', () {
    test('confidencePercent formats correctly', () {
      final s = _makeSignal(confidence: 0.856);
      expect(s.confidencePercent, '86%');
    });

    test('confidencePercent at 0%', () {
      final s = _makeSignal(confidence: 0.0);
      expect(s.confidencePercent, '0%');
    });

    test('isUrgent true when strong + actionable', () {
      final s = _makeSignal(isActionable: true);
      expect(s.isUrgent, isTrue);
    });

    test('isUrgent false when HOLD', () {
      final s = _makeSignal(
        direction: SignalDirection.hold,
        isActionable: false,
      );
      expect(s.isUrgent, isFalse);
    });

    test('isActionable false for HOLD', () {
      final s = _makeSignal(isActionable: false);
      expect(s.isActionable, isFalse);
    });

    test('equality works with Freezed', () {
      final a = _makeSignal();
      final b = _makeSignal();
      expect(a, equals(b));
    });

    test('copyWith preserves other fields', () {
      final s = _makeSignal();
      final updated = s.copyWith(confidence: 0.99);
      expect(updated.confidence, 0.99);
      expect(updated.symbol, s.symbol);
      expect(updated.direction, s.direction);
    });
  });

  // ══════════════════════════════════════════════
  // PositionEntity
  // ══════════════════════════════════════════════

  group('PositionEntity', () {
    final position = PositionEntity(
      symbol: 'AAPL',
      quantity: 10,
      averageEntryPrice: 150.0,
      currentPrice: 165.0,
      unrealizedPnL: 150.0,
      updatedAt: DateTime(2024),
    );

    test('unrealizedPnLPercent calculated correctly', () {
      // (150 / 1500) * 100 = 10%
      expect(position.unrealizedPnLPercent, closeTo(10.0, 0.01));
    });

    test('isProfitable true when pnl positive', () {
      expect(position.isProfitable, isTrue);
    });

    test('isProfitable false when pnl negative', () {
      final losing = position.copyWith(unrealizedPnL: -50);
      expect(losing.isProfitable, isFalse);
    });

    test('marketValue = currentPrice * quantity', () {
      expect(position.marketValue, closeTo(165.0 * 10, 0.001));
    });

    test('unrealizedPnLPercent zero when entry is zero', () {
      final zero = position.copyWith(averageEntryPrice: 0);
      expect(zero.unrealizedPnLPercent, 0.0);
    });
  });

  // ══════════════════════════════════════════════
  // PortfolioEntity
  // ══════════════════════════════════════════════

  group('PortfolioEntity', () {
    final portfolio = PortfolioEntity(
      equity: 105_000.0,
      cash: 50_000.0,
      dayPnL: 1250.0,
      dayPnLPercent: 1.2,
      totalPnL: 5_000.0,
      positions: [
        PositionEntity(
          symbol: 'AAPL',
          quantity: 10,
          averageEntryPrice: 150,
          currentPrice: 165,
          unrealizedPnL: 150,
          updatedAt: DateTime(2024),
        ),
        PositionEntity(
          symbol: 'TSLA',
          quantity: 5,
          averageEntryPrice: 200,
          currentPrice: 220,
          unrealizedPnL: 100,
          updatedAt: DateTime(2024),
        ),
      ],
      updatedAt: DateTime(2024),
    );

    test('openPositionsCount equals positions length', () {
      expect(portfolio.openPositionsCount, 2);
    });

    test('positionsValue sums correctly', () {
      // AAPL: 165*10=1650, TSLA: 220*5=1100 → 2750
      expect(portfolio.positionsValue, closeTo(2750, 0.01));
    });

    test('isDayPositive true when dayPnL > 0', () {
      expect(portfolio.isDayPositive, isTrue);
    });

    test('isDayPositive false when dayPnL < 0', () {
      final losing = portfolio.copyWith(dayPnL: -500);
      expect(losing.isDayPositive, isFalse);
    });

    test('empty positions has zero positionsValue', () {
      final empty = portfolio.copyWith(positions: []);
      expect(empty.positionsValue, 0.0);
    });
  });

  // ══════════════════════════════════════════════
  // OrderEntity
  // ══════════════════════════════════════════════

  group('OrderEntity', () {
    final order = OrderEntity(
      id: 'order-1',
      symbol: 'AAPL',
      side: OrderSide.buy,
      status: OrderStatus.submitted,
      quantity: 10,
      price: 150.0,
      strategyId: 'rsi',
      signalId: 'sig-1',
      createdAt: DateTime(2024),
    );

    test('isTerminal false for submitted', () {
      expect(order.isTerminal, isFalse);
    });

    test('isTerminal true for filled', () {
      expect(order.copyWith(status: OrderStatus.filled).isTerminal, isTrue);
    });

    test('isTerminal true for canceled', () {
      expect(order.copyWith(status: OrderStatus.canceled).isTerminal, isTrue);
    });

    test('isTerminal true for rejected', () {
      expect(order.copyWith(status: OrderStatus.rejected).isTerminal, isTrue);
    });

    test('isTerminal false for pending', () {
      expect(order.copyWith(status: OrderStatus.pending).isTerminal, isFalse);
    });

    test('estimatedValue = quantity * price', () {
      expect(order.estimatedValue, closeTo(1500.0, 0.001));
    });
  });

  // ══════════════════════════════════════════════
  // GetSignalUseCase
  // ══════════════════════════════════════════════

  group('GetSignalUseCase', () {
    late MockSignalRepository repo;
    late GetSignalUseCase useCase;

    setUp(() {
      repo = MockSignalRepository();
      useCase = GetSignalUseCase(repo);
    });

    test('returns Right(signal) on success', () async {
      final signal = _makeSignal();
      when(() => repo.getSignal(symbol: any(named: 'symbol'), strategy: any(named: 'strategy')))
          .thenAnswer((_) async => Right(signal));

      final result = await useCase(
          const GetSignalParams(symbol: 'AAPL', strategy: 'rsi'));

      expect(result, Right(signal));
      verify(() => repo.getSignal(symbol: 'AAPL', strategy: 'rsi')).called(1);
    });

    test('returns Left(Failure) on network error', () async {
      when(() => repo.getSignal(symbol: any(named: 'symbol'), strategy: any(named: 'strategy')))
          .thenAnswer((_) async =>
              const Left(NetworkFailure(message: 'no connection')));

      final result = await useCase(
          const GetSignalParams(symbol: 'AAPL', strategy: 'rsi'));

      expect(result, isA<Left<Failure, SignalEntity>>());
    });

    test('passes symbol and strategy correctly to repository', () async {
      when(() => repo.getSignal(
            symbol: any(named: 'symbol'),
            strategy: any(named: 'strategy'),
          )).thenAnswer((_) async => Right(_makeSignal()));

      await useCase(
          const GetSignalParams(symbol: 'TSLA', strategy: 'ma_cross'));

      verify(() => repo.getSignal(symbol: 'TSLA', strategy: 'ma_cross'))
          .called(1);
    });
  });

  // ══════════════════════════════════════════════
  // Failures
  // ══════════════════════════════════════════════

  group('Failures', () {
    test('NetworkFailure has message', () {
      const f = NetworkFailure(message: 'no internet');
      expect(f.message, 'no internet');
    });

    test('ServerFailure carries status code', () {
      const f = ServerFailure(message: 'Internal error', statusCode: 500);
      expect(f.statusCode, 500);
    });

    test('TimeoutFailure has default message', () {
      const f = TimeoutFailure();
      expect(f.message, isNotEmpty);
    });

    test('WebSocketFailure has message', () {
      const f = WebSocketFailure(message: 'disconnected');
      expect(f.message, 'disconnected');
    });
  });
}
