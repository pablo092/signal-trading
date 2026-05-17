import 'dart:async';

import 'package:bloc/bloc.dart';
import 'package:equatable/equatable.dart';

import '../../domain/entities/signal_entity.dart';
import '../../domain/usecases/signal_usecases.dart';
import '../../../../core/errors/failures.dart';

// ══════════════════════════════════════════════
// Events
// ══════════════════════════════════════════════

abstract class SignalEvent extends Equatable {
  const SignalEvent();
  @override
  List<Object?> get props => [];
}

class SignalRequested extends SignalEvent {
  final String symbol;
  final String strategy;
  const SignalRequested({required this.symbol, required this.strategy});
  @override
  List<Object?> get props => [symbol, strategy];
}

class SignalWatchStarted extends SignalEvent {
  const SignalWatchStarted();
}

class SignalWatchStopped extends SignalEvent {
  const SignalWatchStopped();
}

class _SignalReceived extends SignalEvent {
  final SignalEntity signal;
  const _SignalReceived(this.signal);
  @override
  List<Object?> get props => [signal];
}

// ══════════════════════════════════════════════
// States
// ══════════════════════════════════════════════

abstract class SignalState extends Equatable {
  const SignalState();
  @override
  List<Object?> get props => [];
}

class SignalInitial extends SignalState {
  const SignalInitial();
}

class SignalLoading extends SignalState {
  const SignalLoading();
}

class SignalLoaded extends SignalState {
  final SignalEntity signal;
  final List<SignalEntity> history; // last N signals for sparkline
  const SignalLoaded({required this.signal, this.history = const []});
  @override
  List<Object?> get props => [signal, history];
}

class SignalError extends SignalState {
  final String message;
  const SignalError(this.message);
  @override
  List<Object?> get props => [message];
}

// ══════════════════════════════════════════════
// BLoC
// ══════════════════════════════════════════════

/// SignalBloc — manages signal fetching and real-time WebSocket stream.
///
/// Pattern: BLoC separates UI events from business logic.
/// The widget emits events → BLoC processes → emits states → UI rebuilds.
///
/// Dependencies injected via constructor (testable with mocks).
class SignalBloc extends Bloc<SignalEvent, SignalState> {
  final GetSignalUseCase _getSignal;
  final WatchSignalsUseCase _watchSignals;

  StreamSubscription<SignalEntity>? _signalSubscription;
  final List<SignalEntity> _history = [];
  static const int _maxHistory = 20;

  SignalBloc({
    required GetSignalUseCase getSignal,
    required WatchSignalsUseCase watchSignals,
  })  : _getSignal = getSignal,
        _watchSignals = watchSignals,
        super(const SignalInitial()) {
    on<SignalRequested>(_onSignalRequested);
    on<SignalWatchStarted>(_onWatchStarted);
    on<SignalWatchStopped>(_onWatchStopped);
    on<_SignalReceived>(_onSignalReceived);
  }

  Future<void> _onSignalRequested(
    SignalRequested event,
    Emitter<SignalState> emit,
  ) async {
    emit(const SignalLoading());

    final result = await _getSignal(
      GetSignalParams(symbol: event.symbol, strategy: event.strategy),
    );

    result.fold(
      (failure) => emit(SignalError(_mapFailure(failure))),
      (signal) {
        _addToHistory(signal);
        emit(SignalLoaded(signal: signal, history: List.from(_history)));
      },
    );
  }

  void _onWatchStarted(
    SignalWatchStarted event,
    Emitter<SignalState> emit,
  ) {
    _signalSubscription?.cancel();
    _signalSubscription = _watchSignals().listen(
      (signal) => add(_SignalReceived(signal)),
      onError: (Object error) =>
          emit(SignalError('WebSocket error: $error')),
    );
  }

  void _onWatchStopped(
    SignalWatchStopped event,
    Emitter<SignalState> emit,
  ) {
    _signalSubscription?.cancel();
    _signalSubscription = null;
  }

  void _onSignalReceived(
    _SignalReceived event,
    Emitter<SignalState> emit,
  ) {
    _addToHistory(event.signal);
    emit(SignalLoaded(
      signal: event.signal,
      history: List.from(_history),
    ));
  }

  void _addToHistory(SignalEntity signal) {
    _history.add(signal);
    if (_history.length > _maxHistory) _history.removeAt(0);
  }

  String _mapFailure(Failure failure) {
    return switch (failure) {
      TimeoutFailure() => 'Connection timed out. Retrying...',
      NetworkFailure() => 'Network error: ${failure.message}',
      ServerFailure() => 'Server error (${failure.statusCode}): ${failure.message}',
      SignalNotAvailableFailure() => failure.message,
      _ => failure.message,
    };
  }

  @override
  Future<void> close() {
    _signalSubscription?.cancel();
    return super.close();
  }
}
