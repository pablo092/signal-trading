import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:fl_chart/fl_chart.dart';
import 'package:intl/intl.dart';

import '../../../../core/config.dart';
import '../../../../core/network/engine_http.dart';
import '../../../../injection_container.dart';
import '../../../../shared/theme/trading_theme.dart';
import '../../../../shared/widgets/trading_widgets.dart';
import '../../../orders/presentation/bloc/intents_bloc.dart';
import '../../../orders/presentation/pages/intents_tabs.dart';
import '../../../orders/presentation/widgets/intent_widgets.dart';
import '../../../portfolio/data/portfolio_api.dart';
import '../../../signals/domain/entities/signal_entity.dart';
import '../../../signals/presentation/bloc/signal_bloc.dart';

final _money = NumberFormat('#,##0.00');

class DashboardPage extends StatefulWidget {
  const DashboardPage({super.key});

  @override
  State<DashboardPage> createState() => _DashboardPageState();
}

class _DashboardPageState extends State<DashboardPage> {
  String _selectedSymbol = 'AAPL';
  String _selectedStrategy = 'rsi';
  int _tab = 0;

  /// null when no portfolio source is available (no token, not the web demo):
  /// the portfolio sections then show the "connect a token" state.
  Future<PortfolioSnapshot>? _portfolio;

  final List<String> _symbols = ['AAPL', 'TSLA', 'MSFT', 'NVDA', 'AMZN'];
  final List<String> _strategies = ['rsi', 'ma_cross'];

  @override
  void initState() {
    super.initState();
    _portfolio = _loadPortfolio();
    _fetchSignal();
    if (AppConfig.liveFeedEnabled) {
      context.read<SignalBloc>().add(const SignalWatchStarted());
    }
    if (AppConfig.engineEnabled) {
      final intents = context.read<IntentsBloc>();
      intents.add(const IntentsRequested());
      if (AppConfig.liveFeedEnabled) intents.add(const IntentsWatchStarted());
    }
  }

  Future<PortfolioSnapshot>? _loadPortfolio() {
    final api = sl<PortfolioApi>();
    return api.available ? api.fetch() : null;
  }

  void _fetchSignal() {
    context.read<SignalBloc>().add(SignalRequested(
      symbol: _selectedSymbol,
      strategy: _selectedStrategy,
    ));
  }

  void _refreshPortfolio() {
    setState(() => _portfolio = _loadPortfolio());
  }

  /// "Propose trade" from a signal card: create an intent, show the result.
  void _proposeTrade(SignalEntity signal) {
    context.read<IntentsBloc>().add(IntentProposed(
          symbol: signal.symbol,
          strategy: _selectedStrategy,
        ));
    setState(() => _tab = 3);
  }

  Future<void> _refreshAll() async {
    _fetchSignal();
    _refreshPortfolio();
    if (AppConfig.engineEnabled) {
      context.read<IntentsBloc>().add(const IntentsRequested());
    }
    final portfolio = _portfolio;
    if (portfolio == null) return;
    try {
      await portfolio;
    } catch (_) {}
  }

  @override
  Widget build(BuildContext context) {
    final Widget content = RefreshIndicator(
      color: TradingTheme.accent,
      backgroundColor: TradingTheme.surface,
      onRefresh: _refreshAll,
      child: ListView(
        padding: const EdgeInsets.all(16),
        children: _buildTab(),
      ),
    );
    return Scaffold(
      backgroundColor: TradingTheme.primary,
      appBar: _buildAppBar(),
      body: AppConfig.engineEnabled
          ? IntentNoticeListener(child: content)
          : content,
      bottomNavigationBar: NavigationBar(
        backgroundColor: TradingTheme.surface,
        indicatorColor: TradingTheme.accent.withOpacity(0.2),
        selectedIndex: _tab,
        onDestinationSelected: (i) => setState(() => _tab = i),
        destinations: const [
          NavigationDestination(
            icon: Icon(Icons.dashboard_outlined),
            selectedIcon: Icon(Icons.dashboard_rounded),
            label: 'Dashboard',
          ),
          NavigationDestination(
            icon: Icon(Icons.show_chart_outlined),
            selectedIcon: Icon(Icons.show_chart_rounded),
            label: 'Signals',
          ),
          NavigationDestination(
            icon: Icon(Icons.account_balance_wallet_outlined),
            selectedIcon: Icon(Icons.account_balance_wallet_rounded),
            label: 'Portfolio',
          ),
          NavigationDestination(
            icon: Icon(Icons.receipt_long_outlined),
            selectedIcon: Icon(Icons.receipt_long_rounded),
            label: 'Orders',
          ),
          NavigationDestination(
            icon: Icon(Icons.verified_user_outlined),
            selectedIcon: Icon(Icons.verified_user_rounded),
            label: 'Approvals',
          ),
        ],
      ),
    );
  }

  List<Widget> _buildTab() {
    switch (_tab) {
      case 1:
        return [
          _controls(),
          const SizedBox(height: 12),
          _SignalSection(onFetch: _fetchSignal, onPropose: _proposeTrade),
          const SizedBox(height: 16),
          const _SignalHistorySection(),
        ];
      case 2:
        return [
          _PortfolioSummarySection(future: _portfolio, onRetry: _refreshPortfolio),
          const SizedBox(height: 16),
          _PositionsSection(future: _portfolio),
        ];
      case 3:
        if (!AppConfig.engineEnabled) {
          return const [
            EngineTokenRequiredCard(feature: 'Orders and trade proposals'),
          ];
        }
        return [
          _controls(refetchSignal: false),
          const SizedBox(height: 12),
          OrdersTab(symbol: _selectedSymbol, strategy: _selectedStrategy),
        ];
      case 4:
        if (!AppConfig.engineEnabled) {
          return const [EngineTokenRequiredCard(feature: 'Approvals')];
        }
        return const [ApprovalsTab()];
      default:
        return [
          const _DemoBanner(),
          _PortfolioSummarySection(future: _portfolio, onRetry: _refreshPortfolio),
          const SizedBox(height: 16),
          _controls(),
          const SizedBox(height: 12),
          _SignalSection(onFetch: _fetchSignal, onPropose: _proposeTrade),
          const SizedBox(height: 16),
          _RecentSignalsSection(),
        ];
    }
  }

  Widget _controls({bool refetchSignal = true}) {
    return _SignalControlsSection(
      selectedSymbol: _selectedSymbol,
      selectedStrategy: _selectedStrategy,
      symbols: _symbols,
      strategies: _strategies,
      onSymbolChanged: (v) {
        setState(() => _selectedSymbol = v);
        if (refetchSignal) _fetchSignal();
      },
      onStrategyChanged: (v) {
        setState(() => _selectedStrategy = v);
        if (refetchSignal) _fetchSignal();
      },
    );
  }

  AppBar _buildAppBar() {
    return AppBar(
      title: Row(
        children: [
          Container(
            width: 8,
            height: 8,
            margin: const EdgeInsets.only(right: 8),
            decoration: BoxDecoration(
              color: TradingTheme.profit,
              shape: BoxShape.circle,
              boxShadow: [BoxShadow(color: TradingTheme.profit.withOpacity(0.5), blurRadius: 6)],
            ),
          ),
          const Text('Trading Bot'),
        ],
      ),
      actions: [
        IconButton(
          tooltip: 'Refresh',
          icon: const Icon(Icons.refresh_rounded),
          onPressed: _refreshAll,
        ),
        IconButton(
          tooltip: 'About',
          icon: const Icon(Icons.info_outline_rounded),
          onPressed: _showAbout,
        ),
      ],
    );
  }

  void _showAbout() {
    final trading = AppConfig.engineEnabled
        ? 'Trades are proposed as order intents; each one needs a human '
            'approval before it is submitted.'
        : 'Order execution is disabled: nothing here can place or propose '
            'an order.';
    showDialog<void>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        backgroundColor: TradingTheme.surface,
        title: Text('About this demo', style: TradingTheme.titleMedium),
        content: Text(
          'Signals are computed live by signal-service from Alpaca market data '
          '(RSI and EMA crossover strategies).\n\n'
          'Portfolio data comes from an Alpaca PAPER account. '
          '$trading\n\n'
          'Signals are algorithmic indicators, not financial advice.',
          style: TradingTheme.bodyMedium,
        ),
        actions: [
          FilledButton(
            onPressed: () => Navigator.pop(dialogContext),
            child: const Text('OK'),
          ),
        ],
      ),
    );
  }
}

// ── Demo banner ───────────────────────────────────────────────

class _DemoBanner extends StatelessWidget {
  const _DemoBanner();

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 12),
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 12),
      decoration: BoxDecoration(
        color: TradingTheme.neutral.withOpacity(0.12),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        children: [
          const Icon(Icons.science_outlined, size: 14, color: TradingTheme.neutral),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              AppConfig.engineEnabled
                  ? 'Paper trading · every trade needs a human approval'
                  : 'Paper trading demo · read-only, trades run from the local app',
              style: TradingTheme.caption.copyWith(color: TradingTheme.neutral),
            ),
          ),
        ],
      ),
    );
  }
}

// ── Portfolio Summary ─────────────────────────────────────────

class _PortfolioSummarySection extends StatelessWidget {
  final Future<PortfolioSnapshot>? future;
  final VoidCallback onRetry;
  const _PortfolioSummarySection({required this.future, required this.onRetry});

  @override
  Widget build(BuildContext context) {
    if (future == null) {
      return const EngineTokenRequiredCard(feature: 'Portfolio');
    }
    return FutureBuilder<PortfolioSnapshot>(
      future: future,
      builder: (context, snap) {
        if (snap.connectionState != ConnectionState.done) {
          return const LoadingCard(height: 140);
        }
        final error = snap.error;
        if (error is ApiException) {
          return FailureCard(
            title: 'Portfolio unavailable',
            failure: error.failure,
            onRetry: onRetry,
          );
        }
        if (snap.hasError || !snap.hasData) {
          return Card(
            child: ListTile(
              leading: const Icon(Icons.cloud_off, color: TradingTheme.loss),
              title: Text('Portfolio unavailable', style: TradingTheme.bodyMedium),
              trailing: TextButton(onPressed: onRetry, child: const Text('Retry')),
            ),
          );
        }
        final p = snap.data!;
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Paper portfolio', style: TradingTheme.caption),
            const SizedBox(height: 4),
            Text('\$${_money.format(p.equity)}', style: TradingTheme.displayLarge),
            const SizedBox(height: 4),
            PnLBadge(value: p.unrealizedPnl),
            const SizedBox(height: 16),
            Row(
              children: [
                Expanded(
                  child: StatCard(
                    label: 'Open positions',
                    value: '${p.positions.length}',
                    icon: Icons.pie_chart_outline_rounded,
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: StatCard(
                    label: 'Market',
                    value: p.marketOpen ? 'Open' : 'Closed',
                    valueColor: p.marketOpen ? TradingTheme.profit : TradingTheme.neutral,
                    icon: Icons.schedule_rounded,
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: StatCard(
                    label: 'Mode',
                    value: p.paperMode ? 'Paper' : 'Live',
                    valueColor: TradingTheme.accent,
                    icon: Icons.shield_outlined,
                  ),
                ),
              ],
            ),
          ],
        );
      },
    );
  }
}

class _PositionsSection extends StatelessWidget {
  final Future<PortfolioSnapshot>? future;
  const _PositionsSection({required this.future});

  @override
  Widget build(BuildContext context) {
    if (future == null) return const SizedBox.shrink();
    return FutureBuilder<PortfolioSnapshot>(
      future: future,
      builder: (context, snap) {
        if (!snap.hasData) return const SizedBox.shrink();
        final positions = snap.data!.positions;
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Text('Open positions', style: TradingTheme.titleMedium),
            ),
            if (positions.isEmpty)
              Card(
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Text(
                    'No open positions in the paper account.',
                    style: TradingTheme.bodyMedium,
                  ),
                ),
              ),
            ...positions.map(
              (pos) => Card(
                margin: const EdgeInsets.only(bottom: 8),
                child: ListTile(
                  title: Text(
                    '${pos.symbol} × ${pos.quantity.toStringAsFixed(0)}',
                    style: TradingTheme.bodyMedium,
                  ),
                  subtitle: Text(
                    'Avg \$${_money.format(pos.averageEntryPrice)} · '
                    'Now \$${_money.format(pos.currentPrice)}',
                    style: TradingTheme.caption,
                  ),
                  trailing: PnLBadge(value: pos.unrealizedPnl),
                ),
              ),
            ),
          ],
        );
      },
    );
  }
}

// ── Signal Controls ───────────────────────────────────────────

class _SignalControlsSection extends StatelessWidget {
  final String selectedSymbol;
  final String selectedStrategy;
  final List<String> symbols;
  final List<String> strategies;
  final ValueChanged<String> onSymbolChanged;
  final ValueChanged<String> onStrategyChanged;

  const _SignalControlsSection({
    required this.selectedSymbol,
    required this.selectedStrategy,
    required this.symbols,
    required this.strategies,
    required this.onSymbolChanged,
    required this.onStrategyChanged,
  });

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          child: _DropdownField(
            label: 'Symbol',
            value: selectedSymbol,
            items: symbols,
            onChanged: onSymbolChanged,
          ),
        ),
        const SizedBox(width: 12),
        Expanded(
          child: _DropdownField(
            label: 'Strategy',
            value: selectedStrategy,
            items: strategies,
            onChanged: onStrategyChanged,
          ),
        ),
      ],
    );
  }
}

class _DropdownField extends StatelessWidget {
  final String label;
  final String value;
  final List<String> items;
  final ValueChanged<String> onChanged;

  const _DropdownField({
    required this.label,
    required this.value,
    required this.items,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return DropdownButtonFormField<String>(
      value: value,
      decoration: InputDecoration(labelText: label),
      dropdownColor: TradingTheme.surfaceVariant,
      style: TradingTheme.bodyMedium,
      items: items
          .map((s) => DropdownMenuItem(value: s, child: Text(s.toUpperCase())))
          .toList(),
      onChanged: (v) { if (v != null) onChanged(v); },
    );
  }
}

// ── Signal Section ─────────────────────────────────────────────

class _SignalSection extends StatelessWidget {
  final VoidCallback onFetch;

  /// Creates an order intent; only called with an engine token.
  final ValueChanged<SignalEntity> onPropose;
  const _SignalSection({required this.onFetch, required this.onPropose});

  @override
  Widget build(BuildContext context) {
    return BlocBuilder<SignalBloc, SignalState>(
      builder: (context, state) {
        final loading = state is SignalLoading;
        final generate = SizedBox(
          width: double.infinity,
          child: FilledButton.icon(
            onPressed: loading ? null : onFetch,
            icon: const Icon(Icons.bolt_rounded),
            label: Text(loading ? 'Generating…' : 'Generate Signal'),
          ),
        );
        final Widget body = switch (state) {
          SignalLoading() => const LoadingCard(height: 200),
          SignalLoaded(:final signal, :final history) => Column(
              children: [
                SignalCard(
                  signal: signal,
                  onPropose: !signal.isActionable
                      ? null
                      : AppConfig.engineEnabled
                          ? () => onPropose(signal)
                          : () => _showTokenRequired(context, signal),
                ),
                if (history.length > 1) ...[
                  const SizedBox(height: 16),
                  _ConfidenceSparkline(signals: history),
                ],
              ],
            ),
          SignalError(:final message) => Card(
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Column(
                  children: [
                    const Icon(Icons.error_outline, color: TradingTheme.loss, size: 32),
                    const SizedBox(height: 8),
                    Text(message, style: TradingTheme.bodyMedium, textAlign: TextAlign.center),
                  ],
                ),
              ),
            ),
          _ => const SizedBox.shrink(),
        };
        return Column(
          children: [
            body,
            const SizedBox(height: 12),
            generate,
          ],
        );
      },
    );
  }

  void _showTokenRequired(BuildContext context, SignalEntity signal) {
    showDialog<void>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        backgroundColor: TradingTheme.surface,
        title: Text(
          '${signal.direction.name.toUpperCase()} ${signal.symbol}',
          style: TradingTheme.titleMedium.copyWith(
            color: TradingTheme.directionColor(signal.direction.name),
          ),
        ),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Price: \$${signal.price.toStringAsFixed(2)}', style: TradingTheme.bodyMedium),
            Text('Strategy: ${signal.strategyName}', style: TradingTheme.caption),
            Text('Confidence: ${signal.confidencePercent}', style: TradingTheme.caption),
            const SizedBox(height: 12),
            Text(
              'Proposing a trade needs a bot-engine token, which only a local '
              'build may hold (--dart-define=BOT_ENGINE_TOKEN). No order or '
              'intent was created.',
              style: TradingTheme.caption.copyWith(color: TradingTheme.neutral),
            ),
          ],
        ),
        actions: [
          FilledButton(
            onPressed: () => Navigator.pop(dialogContext),
            child: const Text('OK'),
          ),
        ],
      ),
    );
  }
}

// ── Confidence Sparkline ──────────────────────────────────────

class _ConfidenceSparkline extends StatelessWidget {
  final List<SignalEntity> signals;
  const _ConfidenceSparkline({required this.signals});

  @override
  Widget build(BuildContext context) {
    final spots = signals.asMap().entries.map((e) {
      return FlSpot(e.key.toDouble(), e.value.confidence);
    }).toList();

    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Signal Confidence History', style: TradingTheme.caption),
            const SizedBox(height: 12),
            SizedBox(
              height: 80,
              child: LineChart(
                LineChartData(
                  minY: 0,
                  maxY: 1,
                  gridData: const FlGridData(show: false),
                  borderData: FlBorderData(show: false),
                  titlesData: const FlTitlesData(show: false),
                  lineBarsData: [
                    LineChartBarData(
                      spots: spots,
                      isCurved: true,
                      color: TradingTheme.accent,
                      barWidth: 2,
                      dotData: const FlDotData(show: false),
                      belowBarData: BarAreaData(
                        show: true,
                        color: TradingTheme.accent.withOpacity(0.1),
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ── Recent Signals ────────────────────────────────────────────

class _RecentSignalsSection extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return BlocBuilder<SignalBloc, SignalState>(
      builder: (context, state) {
        if (state is! SignalLoaded || state.history.length < 2) {
          return const SizedBox.shrink();
        }
        final recent = state.history.reversed.skip(1).take(5).toList();
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Text('Recent Signals', style: TradingTheme.titleMedium),
            ),
            ...recent.map((s) => _RecentSignalRow(signal: s)),
          ],
        );
      },
    );
  }
}

class _SignalHistorySection extends StatelessWidget {
  const _SignalHistorySection();

  @override
  Widget build(BuildContext context) {
    return BlocBuilder<SignalBloc, SignalState>(
      builder: (context, state) {
        final history = state is SignalLoaded ? state.history : const <SignalEntity>[];
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Text('History (this session)', style: TradingTheme.titleMedium),
            ),
            if (history.isEmpty)
              Text('No signals yet.', style: TradingTheme.caption),
            ...history.reversed.map((s) => _RecentSignalRow(signal: s)),
          ],
        );
      },
    );
  }
}

class _RecentSignalRow extends StatelessWidget {
  final SignalEntity signal;
  const _RecentSignalRow({required this.signal});

  @override
  Widget build(BuildContext context) {
    final color = TradingTheme.directionColor(signal.direction.name);
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: ListTile(
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
        leading: CircleAvatar(
          radius: 18,
          backgroundColor: color.withOpacity(0.15),
          child: Icon(
            signal.direction == SignalDirection.buy
                ? Icons.arrow_upward_rounded
                : signal.direction == SignalDirection.sell
                    ? Icons.arrow_downward_rounded
                    : Icons.remove_rounded,
            color: color,
            size: 16,
          ),
        ),
        title: Text('${signal.symbol} — ${signal.direction.name.toUpperCase()}',
            style: TradingTheme.bodyMedium),
        subtitle: Text(signal.strategyName, style: TradingTheme.caption),
        trailing: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Text(signal.confidencePercent,
                style: TradingTheme.caption.copyWith(
                  color: TradingTheme.confidenceColor(signal.confidence),
                )),
            Text(
              DateFormat('HH:mm').format(signal.generatedAt.toLocal()),
              style: TradingTheme.caption,
            ),
          ],
        ),
      ),
    );
  }
}
