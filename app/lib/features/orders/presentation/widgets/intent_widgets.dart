import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:intl/intl.dart';

import '../../../../core/errors/failures.dart';
import '../../../../shared/theme/trading_theme.dart';
import '../../domain/entities/order_entity.dart';
import '../../domain/entities/order_intent.dart';
import '../bloc/intents_bloc.dart';

final _money = NumberFormat('#,##0.00');
final _qty = NumberFormat('#,##0.######');

// ══════════════════════════════════════════════
// EngineTokenRequiredCard — the disabled state without a token
// ══════════════════════════════════════════════

/// Shown instead of an engine feature when no bot-engine token is set
/// (always on the public web demo). Nothing behind it calls the engine.
class EngineTokenRequiredCard extends StatelessWidget {
  final String feature;
  const EngineTokenRequiredCard({super.key, required this.feature});

  @override
  Widget build(BuildContext context) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(20),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Icon(Icons.lock_outline_rounded, color: TradingTheme.neutral, size: 32),
            const SizedBox(height: 12),
            Text('$feature: connect a token (local only)',
                style: TradingTheme.titleMedium),
            const SizedBox(height: 8),
            const Text(
              'This needs a bot-engine token, which a public web build must '
              'never hold. Run the app locally with '
              '--dart-define=BOT_ENGINE_TOKEN=<token> (and BOT_ENGINE_URL) to '
              'enable it. Signals keep working without it.',
              style: TradingTheme.bodyMedium,
            ),
          ],
        ),
      ),
    );
  }
}

// ══════════════════════════════════════════════
// FailureCard — an error with its code and request id
// ══════════════════════════════════════════════

class FailureCard extends StatelessWidget {
  final String title;
  final Failure failure;
  final VoidCallback? onRetry;

  const FailureCard({
    super.key,
    required this.title,
    required this.failure,
    this.onRetry,
  });

  @override
  Widget build(BuildContext context) {
    final f = failure;
    return Card(
      child: ListTile(
        leading: const Icon(Icons.cloud_off, color: TradingTheme.loss),
        title: Text(title, style: TradingTheme.bodyMedium),
        subtitle: Text(
          f is ApiFailure
              ? '${intentFailureMessage(f)}\n${f.reference}'
              : intentFailureMessage(f),
          style: TradingTheme.caption,
        ),
        isThreeLine: f is ApiFailure,
        trailing: onRetry == null
            ? null
            : TextButton(onPressed: onRetry, child: const Text('Retry')),
      ),
    );
  }
}

// ══════════════════════════════════════════════
// IntentNoticeListener — SnackBars for intent results
// ══════════════════════════════════════════════

class IntentNoticeListener extends StatelessWidget {
  final Widget child;
  const IntentNoticeListener({super.key, required this.child});

  @override
  Widget build(BuildContext context) {
    return BlocListener<IntentsBloc, IntentsState>(
      listenWhen: (prev, curr) =>
          curr.notice != null && prev.notice?.seq != curr.notice?.seq,
      listener: (context, state) {
        final notice = state.notice!;
        final text = notice.reference == null
            ? notice.message
            : '${notice.message}\n${notice.reference}';
        ScaffoldMessenger.of(context)
          ..hideCurrentSnackBar()
          ..showSnackBar(SnackBar(
            content: Text(text),
            backgroundColor:
                notice.isError ? TradingTheme.loss : TradingTheme.surfaceVariant,
            duration: const Duration(seconds: 6),
          ));
      },
      child: child,
    );
  }
}

// ══════════════════════════════════════════════
// IntentStatusChip
// ══════════════════════════════════════════════

class IntentStatusChip extends StatelessWidget {
  final IntentStatus status;
  const IntentStatusChip({super.key, required this.status});

  static Color colorFor(IntentStatus status) => switch (status) {
        IntentStatus.filled || IntentStatus.submitted => TradingTheme.profit,
        IntentStatus.approved => TradingTheme.accent,
        IntentStatus.pendingRisk ||
        IntentStatus.pendingApproval =>
          TradingTheme.neutral,
        IntentStatus.rejected => TradingTheme.loss,
        IntentStatus.canceled ||
        IntentStatus.expired ||
        IntentStatus.unknown =>
          TradingTheme.onSurfaceMuted,
      };

  @override
  Widget build(BuildContext context) {
    final color = colorFor(status);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        status.label.toUpperCase(),
        style: TradingTheme.caption.copyWith(color: color),
      ),
    );
  }
}

// ══════════════════════════════════════════════
// ExpiryCountdown — time left to approve
// ══════════════════════════════════════════════

class ExpiryCountdown extends StatefulWidget {
  final DateTime expiresAt;
  const ExpiryCountdown({super.key, required this.expiresAt});

  @override
  State<ExpiryCountdown> createState() => _ExpiryCountdownState();
}

class _ExpiryCountdownState extends State<ExpiryCountdown> {
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    _timer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final left = widget.expiresAt.difference(DateTime.now());
    if (left <= Duration.zero) {
      return Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.timer_off_outlined, size: 14, color: TradingTheme.loss),
          const SizedBox(width: 4),
          Text('Approval window expired',
              style: TradingTheme.caption.copyWith(color: TradingTheme.loss)),
        ],
      );
    }
    final minutes = left.inMinutes;
    final seconds = (left.inSeconds % 60).toString().padLeft(2, '0');
    final color =
        left.inSeconds < 60 ? TradingTheme.loss : TradingTheme.neutral;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(Icons.timer_outlined, size: 14, color: color),
        const SizedBox(width: 4),
        Text('Expires in $minutes:$seconds',
            style: TradingTheme.caption.copyWith(color: color)),
      ],
    );
  }
}

// ══════════════════════════════════════════════
// RiskChecksList
// ══════════════════════════════════════════════

class RiskChecksList extends StatelessWidget {
  final RiskEvaluation risk;

  /// Only the failed checks (compact lists).
  final bool failedOnly;

  const RiskChecksList({super.key, required this.risk, this.failedOnly = false});

  @override
  Widget build(BuildContext context) {
    final checks = failedOnly ? risk.failedChecks : risk.checks;
    final headerColor = risk.passed ? TradingTheme.profit : TradingTheme.loss;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'Risk checks (${risk.stage}): ${risk.passed ? 'passed' : 'failed'}',
          style: TradingTheme.caption.copyWith(color: headerColor),
        ),
        if (checks.isEmpty && !failedOnly)
          const Text('No individual results reported.', style: TradingTheme.caption),
        ...checks.map(
          (c) => Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Icon(
                  c.passed ? Icons.check_circle_outline : Icons.cancel_outlined,
                  size: 14,
                  color: c.passed ? TradingTheme.profit : TradingTheme.loss,
                ),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    '${c.name}: ${c.detail}',
                    style: TradingTheme.caption.copyWith(
                      color: c.passed ? TradingTheme.onSurfaceMuted : TradingTheme.loss,
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

// ══════════════════════════════════════════════
// IntentCard
// ══════════════════════════════════════════════

class IntentCard extends StatelessWidget {
  final OrderIntent intent;

  /// Buttons under the card (approve / reject / cancel).
  final Widget? actions;

  /// false shows only the failed risk checks.
  final bool showAllChecks;

  const IntentCard({
    super.key,
    required this.intent,
    this.actions,
    this.showAllChecks = true,
  });

  @override
  Widget build(BuildContext context) {
    final i = intent;
    final sideLabel = switch (i.side) {
      OrderSide.buy => 'BUY',
      OrderSide.sell => 'SELL',
      null => 'NO TRADE',
    };
    final sideColor = TradingTheme.directionColor(i.side?.name ?? 'hold');
    final order = i.order;
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Text(sideLabel,
                    style: TradingTheme.titleMedium.copyWith(color: sideColor)),
                const SizedBox(width: 8),
                Text(i.symbol, style: TradingTheme.titleMedium),
                const SizedBox(width: 8),
                Text(i.strategy,
                    style: TradingTheme.caption.copyWith(color: TradingTheme.accent)),
                const Spacer(),
                IntentStatusChip(status: i.status),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              '${_qty.format(i.qty)} × \$${_money.format(i.signal.price)}'
              ' ≈ \$${_money.format(i.estimatedNotional)}',
              style: TradingTheme.bodyMedium,
            ),
            const SizedBox(height: 2),
            Text(
              'Signal ${i.signal.direction.name.toUpperCase()} · confidence '
              '${(i.signal.confidence * 100).toStringAsFixed(0)}% · by '
              '${i.createdBy.id} (${i.createdBy.type}) · '
              '${DateFormat('HH:mm:ss').format(i.createdAt.toLocal())}',
              style: TradingTheme.caption,
            ),
            if (i.isPendingApproval) ...[
              const SizedBox(height: 6),
              ExpiryCountdown(expiresAt: i.expiresAt),
            ],
            if (i.rejectionCode != null) ...[
              const SizedBox(height: 6),
              Text('Rejection: ${i.rejectionCode}',
                  style: TradingTheme.caption.copyWith(color: TradingTheme.loss)),
            ],
            if (i.decidedBy != null) ...[
              const SizedBox(height: 4),
              Text(
                'Decided by ${i.decidedBy!.id}'
                '${i.decisionReason == null ? '' : ': "${i.decisionReason}"'}',
                style: TradingTheme.caption,
              ),
            ],
            if (order != null) ...[
              const SizedBox(height: 4),
              Text(
                'Order ${order.status} · filled ${_qty.format(order.filledQty)}'
                '${order.filledPrice == null ? '' : ' @ \$${_money.format(order.filledPrice!)}'}',
                style: TradingTheme.caption,
              ),
            ],
            const SizedBox(height: 10),
            RiskChecksList(risk: i.risk, failedOnly: !showAllChecks),
            if (actions != null) ...[
              const SizedBox(height: 12),
              const Divider(),
              const SizedBox(height: 12),
              actions!,
            ],
          ],
        ),
      ),
    );
  }
}

// ══════════════════════════════════════════════
// ReasonDialog — asks for an approve / reject reason
// ══════════════════════════════════════════════

/// Returns the entered reason (possibly empty when not [reasonRequired]), or null
/// when dismissed.
Future<String?> showReasonDialog(
  BuildContext context, {
  required String title,
  required String confirmLabel,
  required bool reasonRequired,
  String? note,
}) {
  return showDialog<String>(
    context: context,
    builder: (_) => _ReasonDialog(
      title: title,
      confirmLabel: confirmLabel,
      reasonRequired: reasonRequired,
      note: note,
    ),
  );
}

class _ReasonDialog extends StatefulWidget {
  final String title;
  final String confirmLabel;
  final bool reasonRequired;
  final String? note;

  const _ReasonDialog({
    required this.title,
    required this.confirmLabel,
    required this.reasonRequired,
    this.note,
  });

  @override
  State<_ReasonDialog> createState() => _ReasonDialogState();
}

class _ReasonDialogState extends State<_ReasonDialog> {
  final _controller = TextEditingController();
  String? _error;

  static const int _maxLength = 200;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _submit() {
    final reason = _controller.text.trim();
    if (widget.reasonRequired && reason.isEmpty) {
      setState(() => _error = 'A reason is required.');
      return;
    }
    if (reason.length > _maxLength) {
      setState(() => _error = 'At most $_maxLength characters.');
      return;
    }
    Navigator.pop(context, reason);
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      backgroundColor: TradingTheme.surface,
      title: Text(widget.title, style: TradingTheme.titleMedium),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (widget.note != null) ...[
            Text(widget.note!, style: TradingTheme.caption),
            const SizedBox(height: 12),
          ],
          TextField(
            controller: _controller,
            autofocus: true,
            maxLength: _maxLength,
            style: TradingTheme.bodyMedium,
            decoration: InputDecoration(
              labelText: widget.reasonRequired ? 'Reason' : 'Reason (optional)',
              errorText: _error,
            ),
            onSubmitted: (_) => _submit(),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: _submit,
          child: Text(widget.confirmLabel),
        ),
      ],
    );
  }
}
