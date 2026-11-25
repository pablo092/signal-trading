import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';

import 'core/config.dart';
import 'features/dashboard/presentation/pages/dashboard_page.dart';
import 'features/orders/presentation/bloc/intents_bloc.dart';
import 'features/signals/presentation/bloc/signal_bloc.dart';
import 'injection_container.dart';
import 'shared/theme/trading_theme.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  await setupDependencies(
    baseUrl: AppConfig.apiBaseUrl,
    wsUrl: AppConfig.wsUrl,
  );

  runApp(const TradingBotApp());
}

class TradingBotApp extends StatelessWidget {
  const TradingBotApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MultiBlocProvider(
      providers: [
        BlocProvider<SignalBloc>(
          create: (_) => sl<SignalBloc>(),
        ),
        // Order intents exist only with an engine token (never on the web demo).
        if (AppConfig.engineEnabled)
          BlocProvider<IntentsBloc>(
            create: (_) => sl<IntentsBloc>(),
          ),
      ],
      child: MaterialApp(
        title: 'Trading Bot',
        debugShowCheckedModeBanner: false,
        theme: TradingTheme.darkTheme,
        home: const DashboardPage(),
      ),
    );
  }
}
