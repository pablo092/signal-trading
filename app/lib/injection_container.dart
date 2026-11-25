import 'package:get_it/get_it.dart';
import 'package:http/http.dart' as http;

import 'core/config.dart';
import 'core/network/engine_http.dart';
import 'features/orders/data/datasources/order_intent_remote_data_source.dart';
import 'features/orders/data/repositories/order_intent_repository_impl.dart';
import 'features/orders/domain/repositories/i_order_intent_repository.dart';
import 'features/orders/presentation/bloc/intents_bloc.dart';
import 'features/portfolio/data/portfolio_api.dart';
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
      wsToken: AppConfig.wsToken,
    ),
  );

  // bot-engine REST. Without a token (or on the web demo) every call fails
  // locally with engine_token_missing; the UI does not offer these features.
  sl.registerLazySingleton(
    () => EngineHttp(
      httpClient: sl(),
      baseUrl: AppConfig.engineUrl,
      token: AppConfig.engineEnabled ? AppConfig.engineToken : '',
    ),
  );

  sl.registerLazySingleton(() => OrderIntentRemoteDataSource(sl()));

  sl.registerLazySingleton(
    () => PortfolioApi(
      httpClient: sl(),
      baseUrl: baseUrl,
      engine: sl<EngineHttp>(),
      useDemoProxy: AppConfig.isWebDemo,
    ),
  );

  // ── Repositories ──────────────────────────────────────────
  sl.registerLazySingleton<ISignalRepository>(
    () => SignalRepositoryImpl(sl()),
  );

  sl.registerLazySingleton<IOrderIntentRepository>(
    () => OrderIntentRepositoryImpl(sl(), sl()),
  );

  // ── Use cases ─────────────────────────────────────────────
  sl.registerLazySingleton(() => GetSignalUseCase(sl()));
  sl.registerLazySingleton(() => WatchSignalsUseCase(sl()));

  // ── BLoCs ─────────────────────────────────────────────────
  sl.registerFactory(
    () => SignalBloc(getSignal: sl(), watchSignals: sl()),
  );

  sl.registerFactory(() => IntentsBloc(repository: sl()));
}
