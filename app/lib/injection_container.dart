import 'package:get_it/get_it.dart';
import 'package:http/http.dart' as http;

import 'features/signals/data/datasources/signal_remote_data_source.dart';
import 'features/signals/data/repositories/signal_repository_impl.dart';
import 'features/signals/domain/repositories/i_signal_repository.dart';
import 'features/signals/domain/usecases/signal_usecases.dart';
import 'features/signals/presentation/bloc/signal_bloc.dart';

final sl = GetIt.instance;

Future<void> setupDependencies({
  required String baseUrl,
  required String wsUrl,
}) async {
  // ── External ──────────────────────────────────────────────
  sl.registerLazySingleton(() => http.Client());

  // ── Data sources ──────────────────────────────────────────
  sl.registerLazySingleton(
    () => SignalRemoteDataSource(
      httpClient: sl(),
      baseUrl: baseUrl,
      wsUrl: wsUrl,
    ),
  );

  // ── Repositories ──────────────────────────────────────────
  sl.registerLazySingleton<ISignalRepository>(
    () => SignalRepositoryImpl(sl()),
  );

  // ── Use cases ─────────────────────────────────────────────
  sl.registerLazySingleton(() => GetSignalUseCase(sl()));
  sl.registerLazySingleton(() => WatchSignalsUseCase(sl()));

  // ── BLoCs ─────────────────────────────────────────────────
  sl.registerFactory(
    () => SignalBloc(getSignal: sl(), watchSignals: sl()),
  );
}
