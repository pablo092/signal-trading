// BL-16 consumer tests: the app decodes the canonical examples in contracts/.
// Run with `flutter test` from app/ (the examples are read from ../contracts).
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

import 'package:trading_bot/core/errors/failures.dart';
import 'package:trading_bot/features/orders/domain/entities/order_entity.dart';
import 'package:trading_bot/features/orders/domain/entities/order_intent.dart';
import 'package:trading_bot/features/portfolio/data/portfolio_api.dart';
import 'package:trading_bot/features/signals/data/datasources/ws_events.dart';
import 'package:trading_bot/features/signals/domain/entities/signal_entity.dart';

String example(String name) =>
    File('../contracts/examples/$name').readAsStringSync();

void main() {
  test('signal example decodes into SignalEntity', () {
    final s = SignalEntity.fromJson(
        jsonDecode(example('signal.json')) as Map<String, dynamic>);
    expect(s.id, '6f1c2d3e-4b5a-4c6d-8e7f-9a0b1c2d3e4f');
    expect(s.direction, SignalDirection.buy);
    expect(s.strength, SignalStrength.strong);
    expect(s.price, 189.42);
    expect(s.isActionable, isTrue);
    expect(s.generatedAt.isUtc, isTrue);
  });

  test('signal.received event yields the signal', () {
    final e = WsEvent.tryParse(example('ws-signal-received.json'));
    expect(e, isNotNull);
    expect(e!.type, wsTypeSignalReceived);
    expect(e.signal?.symbol, 'AAPL');
  });

  test('order.placed and kill_switch.changed parse and carry no signal', () {
    final order = WsEvent.tryParse(example('ws-order-placed.json'));
    final ks = WsEvent.tryParse(example('ws-kill-switch-changed.json'));
    expect(order?.type, wsTypeOrderPlaced);
    expect(order?.data['quantity'], 26.396367);
    expect(order?.signal, isNull);
    expect(order?.intent, isNull);
    expect(ks?.type, wsTypeKillSwitchChanged);
    expect(ks?.data['engaged'], isTrue);
    expect(ks?.signal, isNull);
  });

  test('unknown versions and malformed messages are ignored', () {
    final v2 = (jsonDecode(example('ws-signal-received.json'))
        as Map<String, dynamic>)
      ..['v'] = 2;
    expect(WsEvent.tryParse(jsonEncode(v2)), isNull);
    expect(WsEvent.tryParse('not json'), isNull);
    expect(WsEvent.tryParse('{"type":"signal","data":{}}'), isNull); // pre-v1 shape
  });

  test('portfolio examples decode', () {
    final equity =
        jsonDecode(example('portfolio-equity.json')) as Map<String, dynamic>;
    final positions = jsonDecode(example('portfolio-positions.json')) as List;
    final snap = PortfolioSnapshot.fromJson({...equity, 'positions': positions});
    expect(snap.equity, 100000.25);
    expect(snap.paperMode, isTrue);
    expect(snap.positions.length, 2);
    expect(snap.positions[1].quantity, -3);
  });

  test('portfolio equity and positions decode with the direct-read models', () {
    final equity = PortfolioEquity.fromJson(
        jsonDecode(example('portfolio-equity.json')) as Map<String, dynamic>);
    final positions = PositionItem.listFromJson(
        jsonDecode(example('portfolio-positions.json')) as List);
    final snap =
        PortfolioSnapshot.fromParts(equity: equity, positions: positions);
    expect(equity.equity, 100000.25);
    expect(equity.paperMode, isTrue);
    expect(equity.marketOpen, isFalse);
    expect(snap.positions.length, 2);
    expect(snap.positions[0].symbol, 'AAPL');
    expect(snap.orderExecutionEnabled, isFalse);
  });

  test('order-intent example decodes into OrderIntent', () {
    final i = OrderIntent.fromJson(
        jsonDecode(example('order-intent.json')) as Map<String, dynamic>);
    expect(i.id, '7d0c1b2a-3e4f-4a5b-8c6d-7e8f9a0b1c2d');
    expect(i.status, IntentStatus.pendingApproval);
    expect(i.isPendingApproval, isTrue);
    expect(i.symbol, 'AAPL');
    expect(i.strategy, 'rsi');
    expect(i.side, OrderSide.buy);
    expect(i.qty, 2.638522);
    expect(i.estimatedNotional, 499.99);
    expect(i.signal.direction, SignalDirection.buy);
    expect(i.signal.price, 189.5);
    expect(i.signal.confidence, 0.78);
    expect(i.createdBy, const IntentActor(id: 'mcp-gateway', type: 'agent'));
    expect(i.expiresAt, DateTime.utc(2026, 11, 25, 14, 35));
    expect(i.expiresAt.isUtc, isTrue);
    expect(i.decidedBy, isNull);
    expect(i.decidedAt, isNull);
    expect(i.decisionReason, isNull);
    expect(i.rejectionCode, isNull);
    expect(i.risk.passed, isTrue);
    expect(i.risk.stage, 'creation');
    expect(i.risk.checks.length, 2);
    expect(i.risk.checks.first.name, 'kill_switch');
    expect(i.risk.failedChecks, isEmpty);
    expect(i.order, isNull);
  });

  test('order-intent-list example decodes, incl. filled and rejected', () {
    final list = OrderIntentList.fromJson(
        jsonDecode(example('order-intent-list.json')) as Map<String, dynamic>);
    expect(list.intents.length, 2);

    final filled = list.intents[0];
    expect(filled.status, IntentStatus.filled);
    expect(filled.status.isFinal, isTrue);
    expect(filled.decidedBy?.id, 'owner');
    expect(filled.decidedBy?.type, 'human');
    expect(filled.decidedAt, isNotNull);
    expect(filled.decisionReason, 'looks fine');
    expect(filled.risk.stage, 'submission');
    expect(filled.order, isNotNull);
    expect(filled.order!.id, filled.id);
    expect(filled.order!.status, 'filled');
    expect(filled.order!.filledQty, 0.833194);
    expect(filled.order!.filledPrice, 600.02);
    expect(filled.order!.filledAt, isNotNull);

    final rejected = list.intents[1];
    expect(rejected.status, IntentStatus.rejected);
    expect(rejected.side, isNull);
    expect(rejected.qty, 0);
    expect(rejected.estimatedNotional, 0);
    expect(rejected.signal.direction, SignalDirection.hold);
    expect(rejected.rejectionCode, 'risk_failed');
    expect(rejected.risk.passed, isFalse);
    expect(rejected.risk.failedChecks.map((c) => c.name).toList(),
        ['symbol_allowlist', 'signal_actionable']);
    expect(rejected.order, isNull);
  });

  test('intent.updated event yields the intent and no signal', () {
    final e = WsEvent.tryParse(example('ws-intent-updated.json'));
    expect(e, isNotNull);
    expect(e!.type, wsTypeIntentUpdated);
    expect(e.signal, isNull);
    final intent = e.intent;
    expect(intent, isNotNull);
    expect(intent!.id, '7d0c1b2a-3e4f-4a5b-8c6d-7e8f9a0b1c2d');
    expect(intent.status, IntentStatus.pendingApproval);
    expect(intent.risk.checks, isEmpty);
  });

  test('order.filled is recognised; a malformed intent.updated is ignored', () {
    final placed =
        jsonDecode(example('ws-order-placed.json')) as Map<String, dynamic>;
    final filled = WsEvent.tryParse(jsonEncode({...placed, 'type': 'order.filled'}));
    expect(filled?.type, wsTypeOrderFilled);
    expect(filled?.intent, isNull);
    final bad = WsEvent.tryParse(jsonEncode({...placed, 'type': 'intent.updated'}));
    expect(bad, isNotNull);
    expect(bad!.intent, isNull); // order data is not an intent: skipped
  });

  test('error example decodes into ApiFailure', () {
    final f = ApiFailure.fromResponse(403, example('error.json'));
    expect(f.statusCode, 403);
    expect(f.code, 'insufficient_scope');
    expect(f.message, 'the token lacks the scope this route requires');
    expect(f.requestId, 'vercel-3f2a1b0c');
    expect(f.requiredScope, 'portfolio:read');
  });
}
