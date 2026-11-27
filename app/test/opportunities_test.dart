import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:trading_bot/features/orders/domain/entities/order_intent.dart';
import 'package:trading_bot/features/orders/presentation/pages/opportunities_tab.dart';
import 'package:trading_bot/shared/theme/trading_theme.dart';

Widget _wrap(Widget child) => MaterialApp(
      theme: TradingTheme.darkTheme,
      home: Scaffold(body: SingleChildScrollView(child: child)),
    );

OrderIntent _intent({
  String actorId = 'scanner',
  String actorType = 'system',
  String? reason = 'RSI=27.9, oversold',
  bool riskPassed = true,
}) {
  final now = DateTime.now().toUtc();
  return OrderIntent.fromJson({
    'id': 'intent-1',
    'status': 'pending_approval',
    'symbol': 'AAPL',
    'strategy': 'rsi',
    'side': 'buy',
    'qty': 3,
    'estimated_notional': 1065.42,
    'signal': {
      'id': 'sig-1',
      'direction': 'BUY',
      'confidence': 0.78,
      'price': 355.14,
      'generated_at': now.toIso8601String(),
      if (reason != null) 'reason': reason,
    },
    'created_by': {'id': actorId, 'type': actorType},
    'created_at': now.toIso8601String(),
    'updated_at': now.toIso8601String(),
    'expires_at': now.add(const Duration(minutes: 5)).toIso8601String(),
    'risk': {
      'passed': riskPassed,
      'stage': 'creation',
      'evaluated_at': now.toIso8601String(),
      'checks': [
        const {'name': 'market_open', 'passed': true, 'detail': 'open'},
        {'name': 'notional_cap', 'passed': riskPassed, 'detail': 'ok'},
      ],
    },
  });
}

void main() {
  group('OrderIntent', () {
    test('fromScanner is true only for the scanner system actor', () {
      expect(_intent().fromScanner, isTrue);
      expect(_intent(actorId: 'owner', actorType: 'human').fromScanner, isFalse);
      expect(_intent(actorType: 'service').fromScanner, isFalse);
    });
  });

  group('OpportunityCard', () {
    testWidgets('says what to do, why, and the risk outcome', (tester) async {
      await tester.pumpWidget(
          _wrap(OpportunityCard(intent: _intent(), busy: false)));

      expect(find.text('Suggested by the market scanner'), findsOneWidget);
      expect(find.textContaining('BUY', findRichText: true), findsWidgets);
      expect(find.textContaining('3 AAPL', findRichText: true), findsOneWidget);
      expect(find.textContaining('1,065.42'), findsOneWidget);
      expect(find.textContaining('RSI=27.9, oversold', findRichText: true),
          findsOneWidget);
      expect(find.textContaining('78 %', findRichText: true), findsOneWidget);
      expect(find.text('All 2 risk checks passed'), findsOneWidget);
      expect(find.text('Approve'), findsOneWidget);
      expect(find.text('Reject'), findsOneWidget);
    });

    testWidgets('falls back to the strategy when there is no reason',
        (tester) async {
      await tester.pumpWidget(_wrap(OpportunityCard(
          intent: _intent(reason: null, actorId: 'owner', actorType: 'human'),
          busy: false)));

      expect(find.text('Proposed by owner'), findsOneWidget);
      expect(find.textContaining('BUY signal from rsi', findRichText: true),
          findsOneWidget);
    });

    testWidgets('shows how many risk checks failed', (tester) async {
      await tester.pumpWidget(_wrap(
          OpportunityCard(intent: _intent(riskPassed: false), busy: false)));
      expect(find.text('1 of 2 risk checks passed'), findsOneWidget);
    });
  });
}
