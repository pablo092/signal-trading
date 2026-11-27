import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';

import '../../../../shared/theme/trading_theme.dart';
import '../../../../shared/widgets/trading_widgets.dart';
import '../../domain/entities/order_intent.dart';
import '../bloc/intents_bloc.dart';
import '../widgets/intent_widgets.dart';

// ══════════════════════════════════════════════
// Orders tab — "Propose trade" and recent intents
// ══════════════════════════════════════════════

/// Requires an [IntentsBloc] above it (only provided with an engine token).
class OrdersTab extends StatelessWidget {
  final String symbol;
  final String strategy;

  const OrdersTab({super.key, required this.symbol, required this.strategy});

  @override
  Widget build(BuildContext context) {
    return BlocBuilder<IntentsBloc, IntentsState>(
      builder: (context, state) {
        final last = state.lastProposed;
        final others = state.recent.where((i) => i.id != last?.id).toList();
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _ProposeTradeCard(
              symbol: symbol,
              strategy: strategy,
              proposing: state.proposing,
            ),
            if (last != null) ...[
              const Padding(
                padding: EdgeInsets.symmetric(vertical: 8),
                child: Text('Latest proposal', style: TradingTheme.titleMedium),
              ),
              IntentCard(
                intent: last,
                actions: last.isPendingApproval
                    ? _CancelButton(intent: last, busy: state.busyIds.contains(last.id))
                    : null,
              ),
            ],
            const Padding(
              padding: EdgeInsets.symmetric(vertical: 8),
              child: Text('Recent intents', style: TradingTheme.titleMedium),
            ),
            if (state.load == IntentsLoad.loading && state.recent.isEmpty)
              const LoadingCard(height: 120)
            else if (state.load == IntentsLoad.failure && state.loadFailure != null)
              FailureCard(
                title: 'Intents unavailable',
                failure: state.loadFailure!,
                onRetry: () =>
                    context.read<IntentsBloc>().add(const IntentsRequested()),
              )
            else if (others.isEmpty)
              const Text('No intents yet.', style: TradingTheme.caption),
            ...others.map((i) => IntentCard(intent: i, showAllChecks: false)),
          ],
        );
      },
    );
  }
}

class _ProposeTradeCard extends StatelessWidget {
  final String symbol;
  final String strategy;
  final bool proposing;

  const _ProposeTradeCard({
    required this.symbol,
    required this.strategy,
    required this.proposing,
  });

  @override
  Widget build(BuildContext context) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text('Propose trade: ${symbol.toUpperCase()} · $strategy',
                style: TradingTheme.titleMedium),
            const SizedBox(height: 8),
            const Text(
              'bot-engine fetches the latest signal, sizes the order and runs '
              'every risk check. Nothing is sent to the broker until a human '
              'approves it (Approvals tab). Paper account only.',
              style: TradingTheme.caption,
            ),
            const SizedBox(height: 12),
            FilledButton.icon(
              onPressed: proposing
                  ? null
                  : () => context
                      .read<IntentsBloc>()
                      .add(IntentProposed(symbol: symbol, strategy: strategy)),
              icon: const Icon(Icons.send_rounded, size: 16),
              label: Text(proposing ? 'Proposing…' : 'Propose trade'),
            ),
          ],
        ),
      ),
    );
  }
}

class _CancelButton extends StatelessWidget {
  final OrderIntent intent;
  final bool busy;
  const _CancelButton({required this.intent, required this.busy});

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: Alignment.centerRight,
      child: OutlinedButton.icon(
        onPressed: busy
            ? null
            : () => context.read<IntentsBloc>().add(IntentCanceled(intent.id)),
        icon: const Icon(Icons.close, size: 16),
        label: const Text('Cancel proposal'),
      ),
    );
  }
}

// ══════════════════════════════════════════════
// Approvals tab — pending_approval intents (human scope)
// ══════════════════════════════════════════════

/// Requires an [IntentsBloc] above it (only provided with an engine token).
class ApprovalsTab extends StatelessWidget {
  const ApprovalsTab({super.key});

  static const String approvalNote =
      'Approving needs a human token with the intent:approve scope. '
      'Every risk check runs again before submission, and the engine may '
      'have order execution disabled: then approval is refused and nothing '
      'reaches the broker.';

  @override
  Widget build(BuildContext context) {
    return BlocBuilder<IntentsBloc, IntentsState>(
      builder: (context, state) {
        final pending = state.pending;
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Container(
              padding: const EdgeInsets.all(12),
              margin: const EdgeInsets.only(bottom: 12),
              decoration: BoxDecoration(
                color: TradingTheme.neutral.withValues(alpha: 0.12),
                borderRadius: BorderRadius.circular(8),
              ),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Icon(Icons.verified_user_outlined,
                      size: 16, color: TradingTheme.neutral),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      approvalNote,
                      style: TradingTheme.caption
                          .copyWith(color: TradingTheme.neutral),
                    ),
                  ),
                ],
              ),
            ),
            Row(
              children: [
                Expanded(
                  child: Text('Waiting for approval (${pending.length})',
                      style: TradingTheme.titleMedium),
                ),
                IconButton(
                  tooltip: 'Reload',
                  icon: const Icon(Icons.refresh_rounded),
                  onPressed: () =>
                      context.read<IntentsBloc>().add(const IntentsRequested()),
                ),
              ],
            ),
            const SizedBox(height: 8),
            if (state.load == IntentsLoad.loading && pending.isEmpty)
              const LoadingCard(height: 120)
            else if (state.load == IntentsLoad.failure && state.loadFailure != null)
              FailureCard(
                title: 'Intents unavailable',
                failure: state.loadFailure!,
                onRetry: () =>
                    context.read<IntentsBloc>().add(const IntentsRequested()),
              )
            else if (pending.isEmpty)
              const Card(
                child: Padding(
                  padding: EdgeInsets.all(16),
                  child: Text('Nothing is waiting for approval.',
                      style: TradingTheme.bodyMedium),
                ),
              ),
            ...pending.map(
              (i) => IntentCard(
                intent: i,
                actions: IntentDecisionButtons(
                  intent: i,
                  busy: state.busyIds.contains(i.id),
                ),
              ),
            ),
          ],
        );
      },
    );
  }
}

/// Reject / Approve for a pending intent, each behind a confirmation dialog.
class IntentDecisionButtons extends StatelessWidget {
  final OrderIntent intent;
  final bool busy;
  const IntentDecisionButtons(
      {super.key, required this.intent, required this.busy});

  String get _summary {
    final side = intent.side?.name.toUpperCase() ?? 'NO TRADE';
    return '$side ${intent.symbol} (${intent.strategy})';
  }

  Future<void> _approve(BuildContext context) async {
    final bloc = context.read<IntentsBloc>();
    final reason = await showReasonDialog(
      context,
      title: 'Approve $_summary?',
      confirmLabel: 'Approve',
      reasonRequired: false,
      note: '${ApprovalsTab.approvalNote} If the broker outcome is unknown '
          'the intent stays approved: do not approve again.',
    );
    if (reason == null) return;
    bloc.add(IntentApproved(intent.id, reason: reason.isEmpty ? null : reason));
  }

  Future<void> _reject(BuildContext context) async {
    final bloc = context.read<IntentsBloc>();
    final reason = await showReasonDialog(
      context,
      title: 'Reject $_summary?',
      confirmLabel: 'Reject',
      reasonRequired: true,
    );
    if (reason == null || reason.isEmpty) return;
    bloc.add(IntentRejected(intent.id, reason: reason));
  }

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          child: OutlinedButton.icon(
            onPressed: busy ? null : () => _reject(context),
            icon: const Icon(Icons.close, size: 16),
            label: const Text('Reject'),
            style: OutlinedButton.styleFrom(
              foregroundColor: TradingTheme.loss,
              side: const BorderSide(color: TradingTheme.surfaceVariant),
            ),
          ),
        ),
        const SizedBox(width: 12),
        Expanded(
          flex: 2,
          child: FilledButton.icon(
            onPressed: busy ? null : () => _approve(context),
            icon: busy
                ? const SizedBox(
                    width: 14,
                    height: 14,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.check_rounded, size: 16),
            label: const Text('Approve'),
          ),
        ),
      ],
    );
  }
}
