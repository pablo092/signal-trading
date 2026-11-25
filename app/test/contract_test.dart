// BL-16 consumer tests: the app decodes the canonical examples in contracts/.
// Run with `flutter test` from app/ (the examples are read from ../contracts).
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

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
}
