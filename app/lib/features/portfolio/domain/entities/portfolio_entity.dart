import 'package:equatable/equatable.dart';

/// Represents a snapshot of the trader's portfolio.
class PortfolioEntity extends Equatable {
  final double equity;
  final double dayPnL;
  final double dayPnLPercent;
  final int openPositions;
  final int todayTrades;
  final double winRate; // 0.0–1.0
  final DateTime updatedAt;

  const PortfolioEntity({
    required this.equity,
    required this.dayPnL,
    required this.dayPnLPercent,
    required this.openPositions,
    required this.todayTrades,
    required this.winRate,
    required this.updatedAt,
  });

  factory PortfolioEntity.fromJson(Map<String, dynamic> json) {
    return PortfolioEntity(
      equity: (json['equity'] as num).toDouble(),
      dayPnL: (json['day_pnl'] as num).toDouble(),
      dayPnLPercent: (json['day_pnl_percent'] as num).toDouble(),
      openPositions: json['open_positions'] as int,
      todayTrades: json['today_trades'] as int,
      winRate: (json['win_rate'] as num).toDouble(),
      updatedAt: DateTime.parse(json['updated_at'] as String),
    );
  }

  @override
  List<Object?> get props => [
        equity,
        dayPnL,
        dayPnLPercent,
        openPositions,
        todayTrades,
        winRate,
        updatedAt,
      ];
}
