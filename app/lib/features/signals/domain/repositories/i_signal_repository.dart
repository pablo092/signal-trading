import 'package:dartz/dartz.dart';

import '../../../../core/errors/failures.dart';
import '../entities/signal_entity.dart';

/// Outbound port for signal data access (Clean Architecture).
abstract interface class ISignalRepository {
  /// Fetches a single signal from the signal service.
  Future<Either<Failure, SignalEntity>> getSignal(
    String symbol,
    String strategy,
  );

  /// Returns a live stream of signals pushed over WebSocket.
  Stream<SignalEntity> watchSignals();
}
