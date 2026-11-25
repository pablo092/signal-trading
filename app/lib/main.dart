import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';

import 'features/dashboard/presentation/pages/dashboard_page.dart';
import 'features/signals/presentation/bloc/signal_bloc.dart';
import 'injection_container.dart';
import 'shared/theme/trading_theme.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // 'same-origin' = call the hosting site (the Vercel demo proxy under /api).
  const configuredBaseUrl = String.fromEnvironment(
    'SIGNAL_SERVICE_URL',
    defaultValue: 'http://localhost:8000',
  );
  await setupDependencies(
    baseUrl: configuredBaseUrl == 'same-origin'
        ? Uri.base.origin
        : configuredBaseUrl,
    // An empty BOT_ENGINE_WS_URL disables the live feed (web demo, see BL-15).
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
