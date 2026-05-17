import 'package:equatable/equatable.dart';

enum OrderSide { buy, sell }

enum OrderStatus { pending, submitted, filled, canceled, rejected }

/// Represents a trade order placed with the broker.
class OrderEntity extends Equatable {
  final String id;
  final String symbol;
  final OrderSide side;
  final double quantity;
  final OrderStatus status;
  final String? brokerID;
  final double? filledPrice;
  final String strategyId;
  final String signalId;
  final DateTime createdAt;

  const OrderEntity({
    required this.id,
    required this.symbol,
    required this.side,
    required this.quantity,
    required this.status,
    this.brokerID,
    this.filledPrice,
    required this.strategyId,
    required this.signalId,
    required this.createdAt,
  });

  factory OrderEntity.fromJson(Map<String, dynamic> json) {
    return OrderEntity(
      id: json['id'] as String,
      symbol: json['symbol'] as String,
      side: OrderSide.values.firstWhere(
        (s) => s.name == (json['side'] as String).toLowerCase(),
      ),
      quantity: (json['quantity'] as num).toDouble(),
      status: OrderStatus.values.firstWhere(
        (s) => s.name == (json['status'] as String).toLowerCase(),
      ),
      brokerID: json['broker_id'] as String?,
      filledPrice: json['filled_price'] != null
          ? (json['filled_price'] as num).toDouble()
          : null,
      strategyId: json['strategy_id'] as String,
      signalId: json['signal_id'] as String,
      createdAt: DateTime.parse(json['created_at'] as String),
    );
  }

  @override
  List<Object?> get props => [id, symbol, side, quantity, status, createdAt];
}
