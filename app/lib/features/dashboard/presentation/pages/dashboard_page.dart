import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:fl_chart/fl_chart.dart';
import 'package:intl/intl.dart';

import '../../../signals/domain/entities/signal_entity.dart';
import '../../../signals/presentation/bloc/signal_bloc.dart';
import '../../../../shared/theme/trading_theme.dart';
import '../../../../shared/widgets/trading_widgets.dart';

class DashboardPage extends StatefulWidget {
  const DashboardPage({super.key});

  @override
  State<DashboardPage> createState() => _DashboardPageState();
}

class _DashboardPageState extends State<DashboardPage> {
  String _selectedSymbol = 'AAPL';
  String _selectedStrategy = 'rsi';
  bool _wsConnected = false;

  final List<String> _symbols = ['AAPL', 'TSLA', 'MSFT', 'NVDA', 'AMZN'];
  final List<String> _strategies = ['rsi', 'ma_cross'];

  @override
  void initState() {
    super.initState();
    _fetchSignal();
    context.read<SignalBloc>().add(const SignalWatchStarted());
  }

  void _fetchSignal() {
    context.read<SignalBloc>().add(SignalRequested(
      symbol: _selectedSymbol,
      strategy: _selectedStrategy,
    ));
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: TradingTheme.primary,
      appBar: _buildAppBar(),
      body: RefreshIndicator(
        color: TradingTheme.accent,
        backgroundColor: TradingTheme.surface,
        onRefresh: () async => _fetchSignal(),
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            ConnectionStatusBanner(isConnected: _wsConnected),
            if (!_wsConnected) const SizedBox(height: 8),
            _PortfolioSummarySection(),
            const SizedBox(height: 16),
            _SignalControlsSection(
              selectedSymbol: _selectedSymbol,
              selectedStrategy: _selectedStrategy,
              symbols: _symbols,
              strategies: _strategies,
              onSymbolChanged: (v) => setState(() {
                _selectedSymbol = v;
                _fetchSignal();
              }),
              onStrategyChanged: (v) => setState(() {
                _selectedStrategy = v;
                _fetchSignal();
              }),
            ),
            const SizedBox(height: 12),
            _SignalSection(onFetch: _fetchSignal),
            const SizedBox(height: 16),
            _RecentSignalsSection(),
          ],
        ),
      ),
      bottomNavigationBar: _buildBottomNav(),
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
          icon: const Icon(Icons.notifications_none_rounded),
          onPressed: () {},
        ),
        IconButton(
          icon: const Icon(Icons.settings_outlined),
          onPressed: () {},
        ),
      ],
    );
  }

  Widget _buildBottomNav() {
    return NavigationBar(
      backgroundColor: TradingTheme.surface,
      indicatorColor: TradingTheme.accent.withOpacity(0.2),
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
      ],
    );
  }
}

// ── Portfolio Summary ─────────────────────────────────────────

class _PortfolioSummarySection extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    // In production this would come from a PortfolioBloc
    const equity = 100250.00;
    const dayPnL = 1240.50;
    const dayPnLPct = 1.25;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Portfolio', style: TradingTheme.caption),
        const SizedBox(height: 4),
        Text(
          '\$${NumberFormat('#,##0.00').format(equity)}',
          style: TradingTheme.displayLarge,
        ),
        const SizedBox(height: 4),
        PnLBadge(value: dayPnL, percent: dayPnLPct),
        const SizedBox(height: 16),
        Row(
          children: [
            Expanded(
              child: StatCard(
                label: 'Open positions',
                value: '3',
                icon: Icons.pie_chart_outline_rounded,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: StatCard(
                label: "Today's trades",
                value: '7',
                icon: Icons.swap_horiz_rounded,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: StatCard(
                label: 'Win rate',
                value: '71%',
                valueColor: TradingTheme.profit,
                icon: Icons.emoji_events_outlined,
              ),
            ),
          ],
        ),
      ],
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
  const _SignalSection({required this.onFetch});

  @override
  Widget build(BuildContext context) {
    return BlocBuilder<SignalBloc, SignalState>(
      builder: (context, state) {
        return switch (state) {
          SignalInitial() => FilledButton.icon(
              onPressed: onFetch,
              icon: const Icon(Icons.bolt_rounded),
              label: const Text('Generate Signal'),
            ),
          SignalLoading() => const LoadingCard(height: 200),
          SignalLoaded(:final signal, :final history) => Column(
              children: [
                SignalCard(
                  signal: signal,
                  onApprove: signal.isActionable
                      ? () => _showApproveDialog(context, signal)
                      : null,
                  onDismiss: signal.isActionable ? () {} : null,
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
                    const SizedBox(height: 12),
                    FilledButton(onPressed: onFetch, child: const Text('Retry')),
                  ],
                ),
              ),
            ),
          _ => const SizedBox.shrink(),
        };
      },
    );
  }

  void _showApproveDialog(BuildContext context, SignalEntity signal) {
    showDialog<void>(
      context: context,
      builder: (_) => AlertDialog(
        backgroundColor: TradingTheme.surface,
        title: Text('Confirm Order', style: TradingTheme.titleMedium),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              '${signal.direction.name.toUpperCase()} ${signal.symbol}',
              style: TradingTheme.titleMedium.copyWith(
                color: TradingTheme.directionColor(signal.direction.name),
              ),
            ),
            const SizedBox(height: 8),
            Text('Price: \$${signal.price.toStringAsFixed(2)}', style: TradingTheme.bodyMedium),
            Text('Strategy: ${signal.strategyName}', style: TradingTheme.caption),
            Text('Confidence: ${signal.confidencePercent}', style: TradingTheme.caption),
            const SizedBox(height: 12),
            Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: TradingTheme.neutral.withOpacity(0.1),
                borderRadius: BorderRadius.circular(8),
                border: Border.all(color: TradingTheme.neutral.withOpacity(0.3)),
              ),
              child: Row(
                children: [
                  const Icon(Icons.info_outline, color: TradingTheme.neutral, size: 16),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      'This will submit a paper trading order (no real money).',
                      style: TradingTheme.caption.copyWith(color: TradingTheme.neutral),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Place Order'),
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

// ── Recent Signals List ───────────────────────────────────────

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
