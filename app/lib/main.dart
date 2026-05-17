import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';

import 'features/dashboard/presentation/pages/dashboard_page.dart';
import 'features/signals/presentation/bloc/signal_bloc.dart';
import 'injection_container.dart';
import 'shared/theme/trading_theme.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  await setupDependencies(
    baseUrl: const String.fromEnvironment(
      'SIGNAL_SERVICE_URL',
      defaultValue: 'http://localhost:8000',
    ),
    wsUrl: const String.fromEnvironment(
      'BOT_ENGINE_WS_URL',
      defaultValue: 'ws://localhost:8080/ws',
    ),
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
