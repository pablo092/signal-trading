import 'package:dartz/dartz.dart';
import 'package:equatable/equatable.dart';

import '../../../../core/errors/failures.dart';
import '../entities/signal_entity.dart';
import '../repositories/i_signal_repository.dart';

/// Parameters for [GetSignalUseCase].
class GetSignalParams extends Equatable {
  final String symbol;
  final String strategy;

  const GetSignalParams({required this.symbol, required this.strategy});

  @override
  List<Object?> get props => [symbol, strategy];
}

/// Fetches a trading signal for a given symbol and strategy.
///
/// Returns [Either<Failure, SignalEntity>] following the Railway pattern.
class GetSignalUseCase {
  final ISignalRepository _repository;

  const GetSignalUseCase(this._repository);

  Future<Either<Failure, SignalEntity>> call(GetSignalParams params) {
    return _repository.getSignal(params.symbol, params.strategy);
  }
}

/// Returns a live [Stream<SignalEntity>] of signals pushed via WebSocket.
class WatchSignalsUseCase {
  final ISignalRepository _repository;

  const WatchSignalsUseCase(this._repository);

  Stream<SignalEntity> call() => _repository.watchSignals();
}
