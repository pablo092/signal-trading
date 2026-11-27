import 'package:equatable/equatable.dart';

import '../../../signals/domain/entities/signal_entity.dart';
import 'order_entity.dart';

/// Lifecycle of an order intent (contracts/schemas/order-intent.schema.json).
enum IntentStatus {
  pendingRisk('pending_risk'),
  pendingApproval('pending_approval'),
  approved('approved'),
  submitted('submitted'),
  filled('filled'),
  rejected('rejected'),
  canceled('canceled'),
  expired('expired'),

  /// A status this app version does not know yet.
  unknown('unknown');

  final String wire;
  const IntentStatus(this.wire);

  static IntentStatus fromWire(String value) => IntentStatus.values.firstWhere(
        (s) => s.wire == value,
        orElse: () => IntentStatus.unknown,
      );

  /// No further transition will happen.
  bool get isFinal =>
      this == filled || this == rejected || this == canceled || this == expired;

  String get label => wire.replaceAll('_', ' ');
}

double _num(Object? v) => (v as num).toDouble();

DateTime? _dateOrNull(Object? v) => v == null ? null : DateTime.parse(v as String);

/// Who created or decided an intent.
class IntentActor extends Equatable {
  final String id;

  /// agent | human | service | system
  final String type;

  const IntentActor({required this.id, required this.type});

  factory IntentActor.fromJson(Map<String, dynamic> json) => IntentActor(
        id: json['id'] as String,
        type: json['type'] as String,
      );

  @override
  List<Object?> get props => [id, type];
}

/// The signal the engine fetched when the intent was created.
class IntentSignal extends Equatable {
  final String id;
  final SignalDirection direction;
  final double confidence;
  final double price;
  final DateTime generatedAt;

  /// The strategy's explanation (e.g. "RSI=27.9, oversold"); null on intents
  /// created before the engine recorded it.
  final String? reason;

  const IntentSignal({
    required this.id,
    required this.direction,
    required this.confidence,
    required this.price,
    required this.generatedAt,
    this.reason,
  });

  factory IntentSignal.fromJson(Map<String, dynamic> json) => IntentSignal(
        id: json['id'] as String,
        direction: SignalDirection.values.firstWhere(
          (d) => d.name == (json['direction'] as String).toLowerCase(),
        ),
        confidence: _num(json['confidence']),
        price: _num(json['price']),
        generatedAt: DateTime.parse(json['generated_at'] as String),
        reason: json['reason'] as String?,
      );

  @override
  List<Object?> get props =>
      [id, direction, confidence, price, generatedAt, reason];
}

/// One risk check result (ADR-005 §5.5).
class RiskCheck extends Equatable {
  final String name;
  final bool passed;
  final String detail;

  const RiskCheck({
    required this.name,
    required this.passed,
    required this.detail,
  });

  factory RiskCheck.fromJson(Map<String, dynamic> json) => RiskCheck(
        name: json['name'] as String,
        passed: json['passed'] as bool,
        detail: json['detail'] as String,
      );

  @override
  List<Object?> get props => [name, passed, detail];
}

/// The latest risk evaluation: at creation, then again before submission.
class RiskEvaluation extends Equatable {
  final bool passed;

  /// creation | submission
  final String stage;
  final DateTime evaluatedAt;
  final List<RiskCheck> checks;

  const RiskEvaluation({
    required this.passed,
    required this.stage,
    required this.evaluatedAt,
    required this.checks,
  });

  factory RiskEvaluation.fromJson(Map<String, dynamic> json) => RiskEvaluation(
        passed: json['passed'] as bool,
        stage: json['stage'] as String,
        evaluatedAt: DateTime.parse(json['evaluated_at'] as String),
        checks: (json['checks'] as List)
            .cast<Map<String, dynamic>>()
            .map(RiskCheck.fromJson)
            .toList(),
      );

  List<RiskCheck> get failedChecks => checks.where((c) => !c.passed).toList();

  @override
  List<Object?> get props => [passed, stage, evaluatedAt, checks];
}

/// The broker order, once the intent was submitted.
class IntentOrder extends Equatable {
  final String id;
  final String brokerId;

  /// pending | submitted | partially_filled | filled | canceled | rejected | expired
  final String status;
  final double filledQty;
  final double? filledPrice;
  final DateTime? filledAt;

  const IntentOrder({
    required this.id,
    required this.brokerId,
    required this.status,
    required this.filledQty,
    this.filledPrice,
    this.filledAt,
  });

  factory IntentOrder.fromJson(Map<String, dynamic> json) => IntentOrder(
        id: json['id'] as String,
        brokerId: json['broker_id'] as String,
        status: json['status'] as String,
        filledQty: _num(json['filled_qty']),
        filledPrice:
            json['filled_price'] == null ? null : _num(json['filled_price']),
        filledAt: _dateOrNull(json['filled_at']),
      );

  @override
  List<Object?> get props =>
      [id, brokerId, status, filledQty, filledPrice, filledAt];
}

/// A proposed order (ADR-005 §5.3). Amounts are JSON numbers printed from
/// decimals; like the rest of the app they are held as doubles for display.
class OrderIntent extends Equatable {
  final String id;
  final IntentStatus status;
  final String symbol;
  final String strategy;

  /// null when the signal was not actionable (HOLD).
  final OrderSide? side;
  final double qty;
  final double estimatedNotional;
  final IntentSignal signal;
  final IntentActor createdBy;
  final DateTime createdAt;
  final DateTime updatedAt;
  final DateTime expiresAt;
  final IntentActor? decidedBy;
  final DateTime? decidedAt;
  final String? decisionReason;

  /// risk_failed | rejected_by_approver | broker_rejected | kill_switch | null
  final String? rejectionCode;
  final RiskEvaluation risk;
  final IntentOrder? order;

  const OrderIntent({
    required this.id,
    required this.status,
    required this.symbol,
    required this.strategy,
    required this.side,
    required this.qty,
    required this.estimatedNotional,
    required this.signal,
    required this.createdBy,
    required this.createdAt,
    required this.updatedAt,
    required this.expiresAt,
    this.decidedBy,
    this.decidedAt,
    this.decisionReason,
    this.rejectionCode,
    required this.risk,
    this.order,
  });

  factory OrderIntent.fromJson(Map<String, dynamic> json) {
    final side = json['side'] as String?;
    final decidedBy = json['decided_by'];
    final order = json['order'];
    return OrderIntent(
      id: json['id'] as String,
      status: IntentStatus.fromWire(json['status'] as String),
      symbol: json['symbol'] as String,
      strategy: json['strategy'] as String,
      side: side == null
          ? null
          : OrderSide.values.firstWhere((s) => s.name == side.toLowerCase()),
      qty: _num(json['qty']),
      estimatedNotional: _num(json['estimated_notional']),
      signal: IntentSignal.fromJson(json['signal'] as Map<String, dynamic>),
      createdBy: IntentActor.fromJson(json['created_by'] as Map<String, dynamic>),
      createdAt: DateTime.parse(json['created_at'] as String),
      updatedAt: DateTime.parse(json['updated_at'] as String),
      expiresAt: DateTime.parse(json['expires_at'] as String),
      decidedBy: decidedBy == null
          ? null
          : IntentActor.fromJson(decidedBy as Map<String, dynamic>),
      decidedAt: _dateOrNull(json['decided_at']),
      decisionReason: json['decision_reason'] as String?,
      rejectionCode: json['rejection_code'] as String?,
      risk: RiskEvaluation.fromJson(json['risk'] as Map<String, dynamic>),
      order: order == null
          ? null
          : IntentOrder.fromJson(order as Map<String, dynamic>),
    );
  }

  bool get isPendingApproval => status == IntentStatus.pendingApproval;

  /// True when the engine's market scanner proposed it (not a person or agent).
  bool get fromScanner => createdBy.id == 'scanner' && createdBy.type == 'system';

  @override
  List<Object?> get props => [
        id,
        status,
        symbol,
        strategy,
        side,
        qty,
        estimatedNotional,
        signal,
        createdBy,
        createdAt,
        updatedAt,
        expiresAt,
        decidedBy,
        decidedAt,
        decisionReason,
        rejectionCode,
        risk,
        order,
      ];
}

/// GET /api/v1/order-intents (contracts/schemas/order-intent-list.schema.json).
class OrderIntentList extends Equatable {
  final List<OrderIntent> intents;

  const OrderIntentList(this.intents);

  factory OrderIntentList.fromJson(Map<String, dynamic> json) =>
      OrderIntentList(
        (json['intents'] as List)
            .cast<Map<String, dynamic>>()
            .map(OrderIntent.fromJson)
            .toList(),
      );

  @override
  List<Object?> get props => [intents];
}

/// Result of approve: the intent, plus whether the broker outcome is unknown
/// (HTTP 202: the reconciler resolves it; do not retry).
class IntentActionResult extends Equatable {
  final OrderIntent intent;
  final bool outcomeUnknown;

  const IntentActionResult({required this.intent, this.outcomeUnknown = false});

  @override
  List<Object?> get props => [intent, outcomeUnknown];
}
