import 'package:get_it/get_it.dart';
import 'package:http/http.dart' as http;

import 'features/signals/domain/repositories/i_signal_repository.dart';
import 'features/signals/domain/usecases/signal_usecases.dart';
import 'features/signals/presentation/bloc/signal_bloc.dart';

/// Global service locator instance.
final sl = GetIt.instance;

/// Registers all dependencies in the service locator.
///
/// Call once at app startup before [runApp].
Future<void> setupDependencies({
  required String baseUrl,
  required String wsUrl,
}) async {
  // ── External ──────────────────────────────────────────────
  sl.registerLazySingleton(() => http.Client());

  // ── Data sources & repositories ───────────────────────────
  // TODO: register SignalRemoteDataSource(baseUrl, wsUrl, sl())
  // sl.registerLazySingleton<ISignalRepository>(
  //   () => SignalRepositoryImpl(
  //     remoteDataSource: sl(),
  //   ),
  // );

  // ── Use cases ─────────────────────────────────────────────
  // sl.registerLazySingleton(() => GetSignalUseCase(sl()));
  // sl.registerLazySingleton(() => WatchSignalsUseCase(sl()));

  // ── BLoCs ─────────────────────────────────────────────────
  // sl.registerFactory(
  //   () => SignalBloc(getSignal: sl(), watchSignals: sl()),
  // );
}
