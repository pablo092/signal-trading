import 'package:bloc_test/bloc_test.dart';
import 'package:dartz/dartz.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'package:trading_bot/core/errors/failures.dart';
import 'package:trading_bot/features/signals/domain/entities/signal_entity.dart';
import 'package:trading_bot/features/signals/domain/usecases/signal_usecases.dart';
import 'package:trading_bot/features/signals/presentation/bloc/signal_bloc.dart';

// ── Mocks ────────────────────────────────────────────────────

class MockGetSignalUseCase extends Mock implements GetSignalUseCase {}
class MockWatchSignalsUseCase extends Mock implements WatchSignalsUseCase {}

// ── Fixtures ─────────────────────────────────────────────────

SignalEntity _makeSignal({
  String direction = 'buy',
  double confidence = 0.85,
}) =>
    SignalEntity(
      id: 'sig-001',
      symbol: 'AAPL',
      direction: SignalDirection.buy,
      strength: SignalStrength.strong,
      price: 150.0,
      currency: 'USD',
      confidence: confidence,
      reason: 'RSI oversold',
      strategyName: 'RSIStrategy(period=14)',
      isActionable: true,
      generatedAt: DateTime(2024, 1, 1, 10, 0),
    );

void main() {
  late MockGetSignalUseCase mockGetSignal;
  late MockWatchSignalsUseCase mockWatchSignals;

  setUp(() {
    mockGetSignal = MockGetSignalUseCase();
    mockWatchSignals = MockWatchSignalsUseCase();

    registerFallbackValue(const GetSignalParams(symbol: 'AAPL', strategy: 'rsi'));
  });

  SignalBloc buildBloc() => SignalBloc(
        getSignal: mockGetSignal,
        watchSignals: mockWatchSignals,
      );

  group('SignalBloc', () {
    test('initial state is SignalInitial', () {
      when(mockWatchSignals).thenReturn(const Stream.empty());
      expect(buildBloc().state, isA<SignalInitial>());
    });

    // ── SignalRequested ──────────────────────────────────────

    group('SignalRequested', () {
      blocTest<SignalBloc, SignalState>(
        'emits [Loading, Loaded] when signal fetched successfully',
        build: () {
          when(() => mockGetSignal(any()))
              .thenAnswer((_) async => Right(_makeSignal()));
          when(mockWatchSignals).thenReturn(const Stream.empty());
          return buildBloc();
        },
        act: (bloc) => bloc.add(
            const SignalRequested(symbol: 'AAPL', strategy: 'rsi')),
        expect: () => [
          isA<SignalLoading>(),
          isA<SignalLoaded>(),
        ],
      );

      blocTest<SignalBloc, SignalState>(
        'emits [Loading, Loaded] with correct signal data',
        build: () {
          when(() => mockGetSignal(any()))
              .thenAnswer((_) async => Right(_makeSignal(confidence: 0.90)));
          when(mockWatchSignals).thenReturn(const Stream.empty());
          return buildBloc();
        },
        act: (bloc) =>
            bloc.add(const SignalRequested(symbol: 'AAPL', strategy: 'rsi')),
        expect: () => [
          const SignalLoading(),
          predicate<SignalLoaded>((s) =>
              s.signal.symbol == 'AAPL' && s.signal.confidence == 0.90),
        ],
      );

      blocTest<SignalBloc, SignalState>(
        'emits [Loading, Error] when network failure',
        build: () {
          when(() => mockGetSignal(any())).thenAnswer(
              (_) async => const Left(NetworkFailure(message: 'No internet')));
          when(mockWatchSignals).thenReturn(const Stream.empty());
          return buildBloc();
        },
        act: (bloc) =>
            bloc.add(const SignalRequested(symbol: 'AAPL', strategy: 'rsi')),
        expect: () => [
          const SignalLoading(),
          isA<SignalError>(),
        ],
      );

      blocTest<SignalBloc, SignalState>(
        'emits [Loading, Error] on timeout',
        build: () {
          when(() => mockGetSignal(any())).thenAnswer(
              (_) async => const Left(TimeoutFailure()));
          when(mockWatchSignals).thenReturn(const Stream.empty());
          return buildBloc();
        },
        act: (bloc) =>
            bloc.add(const SignalRequested(symbol: 'AAPL', strategy: 'rsi')),
        expect: () => [
          isA<SignalLoading>(),
          predicate<SignalError>((e) => e.message.contains('timed out')),
        ],
      );

      blocTest<SignalBloc, SignalState>(
        'emits [Loading, Error] on server failure',
        build: () {
          when(() => mockGetSignal(any())).thenAnswer(
              (_) async => const Left(
                    ServerFailure(message: 'Internal error', statusCode: 500),
                  ));
          when(mockWatchSignals).thenReturn(const Stream.empty());
          return buildBloc();
        },
        act: (bloc) =>
            bloc.add(const SignalRequested(symbol: 'AAPL', strategy: 'rsi')),
        expect: () => [isA<SignalLoading>(), isA<SignalError>()],
      );

      blocTest<SignalBloc, SignalState>(
        'history grows with each signal',
        build: () {
          when(() => mockGetSignal(any()))
              .thenAnswer((_) async => Right(_makeSignal()));
          when(mockWatchSignals).thenReturn(const Stream.empty());
          return buildBloc();
        },
        act: (bloc) async {
          bloc.add(const SignalRequested(symbol: 'AAPL', strategy: 'rsi'));
          await Future<void>.delayed(const Duration(milliseconds: 50));
          bloc.add(const SignalRequested(symbol: 'AAPL', strategy: 'rsi'));
        },
        expect: () => [
          isA<SignalLoading>(),
          predicate<SignalLoaded>((s) => s.history.length == 1),
          isA<SignalLoading>(),
          predicate<SignalLoaded>((s) => s.history.length == 2),
        ],
      );
    });

    // ── WebSocket watch ──────────────────────────────────────

    group('SignalWatchStarted', () {
      blocTest<SignalBloc, SignalState>(
        'emits Loaded when signal received from stream',
        build: () {
          when(mockWatchSignals)
              .thenReturn(Stream.value(_makeSignal()));
          return buildBloc();
        },
        act: (bloc) => bloc.add(const SignalWatchStarted()),
        wait: const Duration(milliseconds: 100),
        expect: () => [isA<SignalLoaded>()],
      );

      blocTest<SignalBloc, SignalState>(
        'emits Error on WebSocket failure',
        build: () {
          when(mockWatchSignals).thenReturn(
            Stream.error(const WebSocketFailure(message: 'disconnected')),
          );
          return buildBloc();
        },
        act: (bloc) => bloc.add(const SignalWatchStarted()),
        wait: const Duration(milliseconds: 100),
        expect: () => [isA<SignalError>()],
      );
    });

    // ── Close ─────────────────────────────────────────────────

    test('closes without error', () async {
      when(mockWatchSignals).thenReturn(const Stream.empty());
      final bloc = buildBloc();
      await expectLater(bloc.close(), completes);
    });
  });
}
