import 'package:equatable/equatable.dart';

enum SignalDirection { buy, sell, hold }

enum SignalStrength { strong, moderate, weak }

/// Immutable domain entity representing a trading signal from the signal service.
class SignalEntity extends Equatable {
  final String id;
  final String symbol;
  final SignalDirection direction;
  final SignalStrength strength;
  final double price;
  final String currency;
  final double confidence; // 0.0–1.0
  final String reason;
  final String strategyName;
  final bool isActionable;
  final DateTime generatedAt;

  const SignalEntity({
    required this.id,
    required this.symbol,
    required this.direction,
    required this.strength,
    required this.price,
    required this.currency,
    required this.confidence,
    required this.reason,
    required this.strategyName,
    required this.isActionable,
    required this.generatedAt,
  });

  /// Confidence as a formatted percentage string, e.g. "85.6%".
  String get confidencePercent => '${(confidence * 100).toStringAsFixed(1)}%';

  factory SignalEntity.fromJson(Map<String, dynamic> json) {
    return SignalEntity(
      id: json['id'] as String,
      symbol: json['symbol'] as String,
      direction: SignalDirection.values.firstWhere(
        (d) => d.name == (json['direction'] as String).toLowerCase(),
      ),
      strength: SignalStrength.values.firstWhere(
        (s) => s.name == (json['strength'] as String).toLowerCase(),
      ),
      price: (json['price'] as num).toDouble(),
      currency: json['currency'] as String? ?? 'USD',
      confidence: (json['confidence'] as num).toDouble(),
      reason: json['reason'] as String,
      strategyName: json['strategy_name'] as String,
      isActionable: json['is_actionable'] as bool,
      generatedAt: DateTime.parse(json['generated_at'] as String),
    );
  }

  @override
  List<Object?> get props => [
        id,
        symbol,
        direction,
        strength,
        price,
        currency,
        confidence,
        reason,
        strategyName,
        isActionable,
        generatedAt,
      ];
}
