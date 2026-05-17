/// Base failure type for the Either error channel.
sealed class Failure {
  const Failure();
  String get message;
}

class TimeoutFailure extends Failure {
  const TimeoutFailure();
  @override
  String get message => 'The request timed out.';
}

class NetworkFailure extends Failure {
  @override
  final String message;
  const NetworkFailure(this.message);
}

class ServerFailure extends Failure {
  final int statusCode;
  @override
  final String message;
  const ServerFailure({required this.statusCode, required this.message});
}

class SignalNotAvailableFailure extends Failure {
  @override
  final String message;
  const SignalNotAvailableFailure(this.message);
}

class CacheFailure extends Failure {
  @override
  final String message;
  const CacheFailure(this.message);
}
