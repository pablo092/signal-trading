import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../theme/trading_theme.dart';
import '../../features/signals/domain/entities/signal_entity.dart';

// ══════════════════════════════════════════════
// SignalCard — shows a single signal
// ══════════════════════════════════════════════

class SignalCard extends StatelessWidget {
  final SignalEntity signal;
  final VoidCallback? onApprove;
  final VoidCallback? onDismiss;

  const SignalCard({
    super.key,
    required this.signal,
    this.onApprove,
    this.onDismiss,
  });

  @override
  Widget build(BuildContext context) {
    final directionColor = TradingTheme.directionColor(signal.direction.name);
    final directionIcon = switch (signal.direction) {
      SignalDirection.buy => Icons.trending_up_rounded,
      SignalDirection.sell => Icons.trending_down_rounded,
      SignalDirection.hold => Icons.trending_flat_rounded,
    };

    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Header row
            Row(
              children: [
                // Direction badge
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                  decoration: BoxDecoration(
                    color: directionColor.withOpacity(0.15),
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(color: directionColor.withOpacity(0.4)),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(directionIcon, color: directionColor, size: 16),
                      const SizedBox(width: 6),
                      Text(
                        signal.direction.name.toUpperCase(),
                        style: TradingTheme.titleMedium.copyWith(
                          color: directionColor,
                          fontSize: 13,
                        ),
                      ),
                    ],
                  ),
                ),
                const SizedBox(width: 12),
                // Symbol
                Text(signal.symbol, style: TradingTheme.titleMedium),
                const Spacer(),
                // Strength pill
                _StrengthPill(strength: signal.strength),
              ],
            ),
            const SizedBox(height: 12),

            // Price
            Row(
              children: [
                Text(
                  '\$${signal.price.toStringAsFixed(2)}',
                  style: TradingTheme.displayLarge.copyWith(fontSize: 24),
                ),
                const SizedBox(width: 8),
                Text(signal.currency, style: TradingTheme.caption),
              ],
            ),
            const SizedBox(height: 8),

            // Confidence bar
            ConfidenceBar(confidence: signal.confidence),
            const SizedBox(height: 10),

            // Reason
            Text(
              signal.reason,
              style: TradingTheme.caption,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
            const SizedBox(height: 4),
            Text(
              signal.strategyName,
              style: TradingTheme.caption.copyWith(color: TradingTheme.accent),
            ),

            // Action buttons (only for actionable signals)
            if (signal.isActionable && (onApprove != null || onDismiss != null)) ...[
              const SizedBox(height: 16),
              const Divider(),
              const SizedBox(height: 12),
              Row(
                children: [
                  if (onDismiss != null)
                    Expanded(
                      child: OutlinedButton.icon(
                        onPressed: onDismiss,
                        icon: const Icon(Icons.close, size: 16),
                        label: const Text('Dismiss'),
                        style: OutlinedButton.styleFrom(
                          foregroundColor: TradingTheme.onSurfaceMuted,
                          side: const BorderSide(color: TradingTheme.surfaceVariant),
                          shape: RoundedRectangleBorder(
                            borderRadius: BorderRadius.circular(10),
                          ),
                        ),
                      ),
                    ),
                  if (onApprove != null && onDismiss != null) const SizedBox(width: 12),
                  if (onApprove != null)
                    Expanded(
                      flex: 2,
                      child: FilledButton.icon(
                        onPressed: onApprove,
                        icon: const Icon(Icons.check_rounded, size: 16),
                        label: const Text('Approve Order'),
                      ),
                    ),
                ],
              ),
            ],

            // Timestamp
            const SizedBox(height: 10),
            Text(
              DateFormat('HH:mm:ss').format(signal.generatedAt.toLocal()),
              style: TradingTheme.caption,
            ),
          ],
        ),
      ),
    );
  }
}

// ══════════════════════════════════════════════
// ConfidenceBar
// ══════════════════════════════════════════════

class ConfidenceBar extends StatelessWidget {
  final double confidence; // 0.0 to 1.0

  const ConfidenceBar({super.key, required this.confidence});

  @override
  Widget build(BuildContext context) {
    final color = TradingTheme.confidenceColor(confidence);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Text('Confidence', style: TradingTheme.caption),
            Text(
              '${(confidence * 100).toStringAsFixed(0)}%',
              style: TradingTheme.caption.copyWith(color: color),
            ),
          ],
        ),
        const SizedBox(height: 4),
        ClipRRect(
          borderRadius: BorderRadius.circular(4),
          child: LinearProgressIndicator(
            value: confidence,
            minHeight: 6,
            backgroundColor: TradingTheme.surfaceVariant,
            valueColor: AlwaysStoppedAnimation<Color>(color),
          ),
        ),
      ],
    );
  }
}

// ══════════════════════════════════════════════
// PnLBadge — shows P&L with color
// ══════════════════════════════════════════════

class PnLBadge extends StatelessWidget {
  final double value;
  final double? percent;
  final bool large;

  const PnLBadge({
    super.key,
    required this.value,
    this.percent,
    this.large = false,
  });

  @override
  Widget build(BuildContext context) {
    final isPositive = value >= 0;
    final color = isPositive ? TradingTheme.profit : TradingTheme.loss;
    final sign = isPositive ? '+' : '';
    final style = large
        ? TradingTheme.displayLarge.copyWith(color: color, fontSize: 28)
        : TradingTheme.titleMedium.copyWith(color: color);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.end,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text('$sign\$${value.abs().toStringAsFixed(2)}', style: style),
        if (percent != null)
          Text(
            '$sign${percent!.toStringAsFixed(2)}%',
            style: TradingTheme.caption.copyWith(color: color),
          ),
      ],
    );
  }
}

// ══════════════════════════════════════════════
// StatCard — metric card for dashboard
// ══════════════════════════════════════════════

class StatCard extends StatelessWidget {
  final String label;
  final String value;
  final Color? valueColor;
  final IconData? icon;
  final Widget? trailing;

  const StatCard({
    super.key,
    required this.label,
    required this.value,
    this.valueColor,
    this.icon,
    this.trailing,
  });

  @override
  Widget build(BuildContext context) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                if (icon != null) ...[
                  Icon(icon, size: 16, color: TradingTheme.onSurfaceMuted),
                  const SizedBox(width: 6),
                ],
                Text(label, style: TradingTheme.caption),
                if (trailing != null) ...[
                  const Spacer(),
                  trailing!,
                ],
              ],
            ),
            const SizedBox(height: 8),
            Text(
              value,
              style: TradingTheme.titleMedium.copyWith(
                fontSize: 20,
                color: valueColor ?? TradingTheme.onSurface,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ══════════════════════════════════════════════
// _StrengthPill — internal
// ══════════════════════════════════════════════

class _StrengthPill extends StatelessWidget {
  final SignalStrength strength;
  const _StrengthPill({required this.strength});

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (strength) {
      SignalStrength.strong => ('STRONG', TradingTheme.profit),
      SignalStrength.moderate => ('MOD', TradingTheme.neutral),
      SignalStrength.weak => ('WEAK', TradingTheme.onSurfaceMuted),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withOpacity(0.12),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(label, style: TradingTheme.caption.copyWith(color: color)),
    );
  }
}

// ══════════════════════════════════════════════
// LoadingShimmer
// ══════════════════════════════════════════════

class LoadingCard extends StatelessWidget {
  final double height;
  const LoadingCard({super.key, this.height = 140});

  @override
  Widget build(BuildContext context) {
    return Card(
      child: SizedBox(
        height: height,
        child: const Center(
          child: CircularProgressIndicator(
            strokeWidth: 2,
            color: TradingTheme.accent,
          ),
        ),
      ),
    );
  }
}

// ══════════════════════════════════════════════
// ConnectionStatusBanner
// ══════════════════════════════════════════════

class ConnectionStatusBanner extends StatelessWidget {
  final bool isConnected;
  const ConnectionStatusBanner({super.key, required this.isConnected});

  @override
  Widget build(BuildContext context) {
    if (isConnected) return const SizedBox.shrink();
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 16),
      color: TradingTheme.loss.withOpacity(0.15),
      child: Row(
        children: [
          const Icon(Icons.wifi_off, size: 14, color: TradingTheme.loss),
          const SizedBox(width: 8),
          Text(
            'Disconnected — reconnecting...',
            style: TradingTheme.caption.copyWith(color: TradingTheme.loss),
          ),
        ],
      ),
    );
  }
}
