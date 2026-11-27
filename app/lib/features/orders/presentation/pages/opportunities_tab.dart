import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:intl/intl.dart';

import '../../../../core/notifications/browser_notifier.dart';
import '../../../../shared/theme/trading_theme.dart';
import '../../../../shared/widgets/trading_widgets.dart';
import '../../domain/entities/order_intent.dart';
import '../bloc/intents_bloc.dart';
import '../widgets/intent_widgets.dart';
import 'intents_tabs.dart';

final _money = NumberFormat('#,##0.00');
final _qty = NumberFormat('#,##0.######');

// ══════════════════════════════════════════════
// Opportunities: trades waiting for the owner's decision
// ══════════════════════════════════════════════

/// The home screen of a local build: every intent waiting for approval,
/// most of them found by the engine's market scanner, each with what to do,
/// why, and one-tap Approve / Reject. Requires an [IntentsBloc] above it.
class OpportunitiesTab extends StatelessWidget {
  const OpportunitiesTab({super.key});

  @override
  Widget build(BuildContext context) {
    return BlocBuilder<IntentsBloc, IntentsState>(
      builder: (context, state) {
        final pending = [...state.pending]
          ..sort((a, b) => b.createdAt.compareTo(a.createdAt));
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    pending.isEmpty
                        ? 'Opportunities'
                        : 'Opportunities (${pending.length})',
                    style: TradingTheme.titleMedium,
                  ),
                ),
                IconButton(
                  tooltip: 'Reload',
                  icon: const Icon(Icons.refresh_rounded),
                  onPressed: () =>
                      context.read<IntentsBloc>().add(const IntentsRequested()),
                ),
              ],
            ),
            const _NotificationsPrompt(),
            const SizedBox(height: 8),
            if (state.load == IntentsLoad.loading && pending.isEmpty)
              const LoadingCard(height: 160)
            else if (state.load == IntentsLoad.failure &&
                state.loadFailure != null)
              FailureCard(
                title: 'Opportunities unavailable',
                failure: state.loadFailure!,
                onRetry: () =>
                    context.read<IntentsBloc>().add(const IntentsRequested()),
              )
            else if (pending.isEmpty)
              const _NoOpportunitiesCard(),
            ...pending.map(
              (i) => OpportunityCard(
                intent: i,
                busy: state.busyIds.contains(i.id),
              ),
            ),
          ],
        );
      },
    );
  }
}

/// One trade to decide on: what, why, the risk outcome and the deadline.
class OpportunityCard extends StatelessWidget {
  final OrderIntent intent;
  final bool busy;

  const OpportunityCard({super.key, required this.intent, required this.busy});

  @override
  Widget build(BuildContext context) {
    final i = intent;
    final side = i.side?.name.toUpperCase() ?? 'NO TRADE';
    final color = TradingTheme.directionColor(i.side?.name ?? 'hold');
    final checks = i.risk.checks;
    final passed = checks.where((c) => c.passed).length;
    final reason = (i.signal.reason?.trim().isNotEmpty ?? false)
        ? i.signal.reason!
        : '${i.signal.direction.name.toUpperCase()} signal from ${i.strategy}';

    return Card(
      margin: const EdgeInsets.only(bottom: 12),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: color.withValues(alpha: 0.5)),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Icon(Icons.lightbulb_outline_rounded, size: 16, color: color),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    i.fromScanner
                        ? 'Suggested by the market scanner'
                        : 'Proposed by ${i.createdBy.id}',
                    style: TradingTheme.caption,
                  ),
                ),
                ExpiryCountdown(expiresAt: i.expiresAt),
              ],
            ),
            const SizedBox(height: 10),
            Text.rich(
              TextSpan(children: [
                TextSpan(
                  text: '$side ',
                  style: TradingTheme.displayLarge
                      .copyWith(color: color, fontSize: 26),
                ),
                TextSpan(
                  text: '${_qty.format(i.qty)} ${i.symbol}',
                  style: TradingTheme.displayLarge.copyWith(fontSize: 26),
                ),
              ]),
            ),
            const SizedBox(height: 4),
            Text(
              '≈ \$${_money.format(i.estimatedNotional)} at '
              '\$${_money.format(i.signal.price)} per share · paper account',
              style: TradingTheme.bodyMedium
                  .copyWith(color: TradingTheme.onSurfaceMuted),
            ),
            const SizedBox(height: 12),
            _Fact(
              icon: Icons.insights_rounded,
              label: 'Why',
              text: '$reason (${i.strategy}, confidence '
                  '${(i.signal.confidence * 100).round()} %)',
            ),
            const SizedBox(height: 6),
            Theme(
              data:
                  Theme.of(context).copyWith(dividerColor: Colors.transparent),
              child: ExpansionTile(
                tilePadding: EdgeInsets.zero,
                childrenPadding: const EdgeInsets.only(bottom: 8),
                leading: Icon(
                  i.risk.passed
                      ? Icons.verified_rounded
                      : Icons.error_outline_rounded,
                  size: 18,
                  color:
                      i.risk.passed ? TradingTheme.profit : TradingTheme.loss,
                ),
                title: Text(
                  i.risk.passed
                      ? 'All $passed risk checks passed'
                      : '$passed of ${checks.length} risk checks passed',
                  style: TradingTheme.bodyMedium,
                ),
                subtitle: const Text(
                  'They run again when you approve.',
                  style: TradingTheme.caption,
                ),
                children: [RiskChecksList(risk: i.risk)],
              ),
            ),
            const SizedBox(height: 8),
            IntentDecisionButtons(intent: i, busy: busy),
          ],
        ),
      ),
    );
  }
}

class _Fact extends StatelessWidget {
  final IconData icon;
  final String label;
  final String text;
  const _Fact({required this.icon, required this.label, required this.text});

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, size: 18, color: TradingTheme.accent),
        const SizedBox(width: 8),
        Expanded(
          child: Text.rich(
            TextSpan(children: [
              TextSpan(
                text: '$label: ',
                style: TradingTheme.bodyMedium
                    .copyWith(fontWeight: FontWeight.w600),
              ),
              TextSpan(text: text, style: TradingTheme.bodyMedium),
            ]),
          ),
        ),
      ],
    );
  }
}

class _NoOpportunitiesCard extends StatelessWidget {
  const _NoOpportunitiesCard();

  @override
  Widget build(BuildContext context) {
    return const Card(
      child: Padding(
        padding: EdgeInsets.all(20),
        child: Column(
          children: [
            Icon(Icons.radar_rounded, size: 40, color: TradingTheme.accent),
            SizedBox(height: 12),
            Text('No trades to decide right now',
                style: TradingTheme.titleMedium, textAlign: TextAlign.center),
            SizedBox(height: 8),
            Text(
              'The engine watches the market for you: every 5 minutes during '
              'US market hours (09:30–16:00 New York) it asks each strategy '
              'about each allowed symbol. When a signal passes every risk '
              'check it shows up here, and you get an email and a browser '
              'notification. Nothing is traded until you approve it.',
              style: TradingTheme.caption,
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }
}

/// Offers to turn on browser notifications until the browser has an answer.
class _NotificationsPrompt extends StatefulWidget {
  const _NotificationsPrompt();

  @override
  State<_NotificationsPrompt> createState() => _NotificationsPromptState();
}

class _NotificationsPromptState extends State<_NotificationsPrompt> {
  String _permission = BrowserNotifier.permission;

  Future<void> _enable() async {
    final result = await BrowserNotifier.requestPermission();
    if (mounted) setState(() => _permission = result);
  }

  @override
  Widget build(BuildContext context) {
    if (_permission != 'default') return const SizedBox.shrink();
    return Container(
      margin: const EdgeInsets.only(top: 4),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: TradingTheme.accent.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        children: [
          const Icon(Icons.notifications_active_outlined,
              size: 18, color: TradingTheme.accent),
          const SizedBox(width: 8),
          const Expanded(
            child: Text(
              'Get a notification when a new opportunity appears '
              '(while this tab is open).',
              style: TradingTheme.caption,
            ),
          ),
          TextButton(onPressed: _enable, child: const Text('Turn on')),
        ],
      ),
    );
  }
}

/// Shows a browser notification for every newly pending intent. Wrap the
/// page with it; it needs an [IntentsBloc] above it.
class OpportunityNotifier extends StatefulWidget {
  final Widget child;
  const OpportunityNotifier({super.key, required this.child});

  @override
  State<OpportunityNotifier> createState() => _OpportunityNotifierState();
}

class _OpportunityNotifierState extends State<OpportunityNotifier> {
  final Set<String> _notified = {};

  void _notify(IntentsState state) {
    // The first load also lists intents that were already pending: notify
    // only for ones created in the last few minutes, and each one once.
    final recent = DateTime.now().subtract(const Duration(minutes: 6));
    for (final i in state.pending) {
      if (i.createdAt.isBefore(recent) || !_notified.add(i.id)) continue;
      final side = i.side?.name.toUpperCase() ?? 'TRADE';
      BrowserNotifier.show(
        'Opportunity: $side ${_qty.format(i.qty)} ${i.symbol}',
        body: '${i.signal.reason ?? i.strategy} · '
            '≈ \$${_money.format(i.estimatedNotional)} · approve or reject '
            'before it expires',
        tag: i.id,
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return BlocListener<IntentsBloc, IntentsState>(
      listenWhen: (prev, next) => !identical(prev.pending, next.pending),
      listener: (context, state) => _notify(state),
      child: widget.child,
    );
  }
}
