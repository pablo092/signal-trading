import 'package:dartz/dartz.dart';

import '../../../../core/errors/failures.dart';
import '../../domain/entities/signal_entity.dart';
import '../../domain/repositories/i_signal_repository.dart';
import '../datasources/signal_remote_data_source.dart';

class SignalRepositoryImpl implements ISignalRepository {
  final SignalRemoteDataSource _dataSource;

  const SignalRepositoryImpl(this._dataSource);

  @override
  Future<Either<Failure, SignalEntity>> getSignal(
    String symbol,
    String strategy,
  ) async {
    try {
      final signal = await _dataSource.generateSignal(symbol, strategy);
      return Right(signal);
    } on HttpException catch (e) {
      return Left(ServerFailure(
        message: 'Signal service error',
        statusCode: e.statusCode,
      ));
    } on Exception catch (e) {
      if (e.toString().contains('TimeoutException')) {
        return Left(const TimeoutFailure());
      }
      return Left(NetworkFailure(message: e.toString()));
    }
  }

  @override
  Stream<SignalEntity> watchSignals() => _dataSource.watchSignals();
}
