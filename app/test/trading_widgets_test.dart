import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:trading_bot/features/signals/domain/entities/signal_entity.dart';
import 'package:trading_bot/shared/theme/trading_theme.dart';
import 'package:trading_bot/shared/widgets/trading_widgets.dart';

Widget _wrap(Widget child) => MaterialApp(
      theme: TradingTheme.darkTheme,
      home: Scaffold(body: SingleChildScrollView(child: child)),
    );

SignalEntity _makeSignal({
  SignalDirection direction = SignalDirection.buy,
  SignalStrength strength = SignalStrength.strong,
  bool isActionable = true,
  double confidence = 0.85,
}) =>
    SignalEntity(
      id: 'test',
      symbol: 'AAPL',
      direction: direction,
      strength: strength,
      price: 150.0,
      currency: 'USD',
      confidence: confidence,
      reason: 'RSI=22.5 — oversold',
      strategyName: 'RSIStrategy(period=14)',
      isActionable: isActionable,
      generatedAt: DateTime(2024, 1, 1, 10, 30, 0),
    );

void main() {
  group('SignalCard', () {
    testWidgets('shows symbol', (tester) async {
      await tester.pumpWidget(_wrap(SignalCard(signal: _makeSignal())));
      expect(find.text('AAPL'), findsWidgets);
    });

    testWidgets('shows BUY direction', (tester) async {
      await tester.pumpWidget(_wrap(SignalCard(signal: _makeSignal())));
      expect(find.text('BUY'), findsOneWidget);
    });

    testWidgets('shows SELL direction', (tester) async {
      await tester.pumpWidget(
          _wrap(SignalCard(signal: _makeSignal(direction: SignalDirection.sell))));
      expect(find.text('SELL'), findsOneWidget);
    });

    testWidgets('shows price', (tester) async {
      await tester.pumpWidget(_wrap(SignalCard(signal: _makeSignal())));
      expect(find.textContaining('150.00'), findsWidgets);
    });

    testWidgets('shows strategy name', (tester) async {
      await tester.pumpWidget(_wrap(SignalCard(signal: _makeSignal())));
      expect(find.textContaining('RSIStrategy'), findsOneWidget);
    });

    testWidgets('shows STRONG strength pill', (tester) async {
      await tester.pumpWidget(_wrap(SignalCard(signal: _makeSignal())));
      expect(find.text('STRONG'), findsOneWidget);
    });

    testWidgets('shows Approve button when actionable and callback provided',
        (tester) async {
      await tester.pumpWidget(_wrap(
        SignalCard(
          signal: _makeSignal(isActionable: true),
          onApprove: () {},
          onDismiss: () {},
        ),
      ));
      expect(find.text('Approve Order'), findsOneWidget);
      expect(find.text('Dismiss'), findsOneWidget);
    });

    testWidgets('hides buttons when not actionable', (tester) async {
      await tester.pumpWidget(_wrap(
        SignalCard(
          signal: _makeSignal(isActionable: false),
          onApprove: null,
          onDismiss: null,
        ),
      ));
      expect(find.text('Approve Order'), findsNothing);
      expect(find.text('Dismiss'), findsNothing);
    });

    testWidgets('onApprove callback fires on tap', (tester) async {
      var approved = false;
      await tester.pumpWidget(_wrap(
        SignalCard(
          signal: _makeSignal(isActionable: true),
          onApprove: () => approved = true,
          onDismiss: () {},
        ),
      ));
      await tester.tap(find.text('Approve Order'));
      await tester.pump();
      expect(approved, isTrue);
    });

    testWidgets('onDismiss callback fires on tap', (tester) async {
      var dismissed = false;
      await tester.pumpWidget(_wrap(
        SignalCard(
          signal: _makeSignal(isActionable: true),
          onApprove: () {},
          onDismiss: () => dismissed = true,
        ),
      ));
      await tester.tap(find.text('Dismiss'));
      await tester.pump();
      expect(dismissed, isTrue);
    });

    testWidgets('shows MOD for moderate strength', (tester) async {
      await tester.pumpWidget(_wrap(
        SignalCard(signal: _makeSignal(strength: SignalStrength.moderate)),
      ));
      expect(find.text('MOD'), findsOneWidget);
    });
  });

  group('ConfidenceBar', () {
    testWidgets('shows confidence percentage', (tester) async {
      await tester.pumpWidget(_wrap(const ConfidenceBar(confidence: 0.85)));
      expect(find.text('85%'), findsOneWidget);
    });

    testWidgets('shows 0% confidence', (tester) async {
      await tester.pumpWidget(_wrap(const ConfidenceBar(confidence: 0.0)));
      expect(find.text('0%'), findsOneWidget);
    });

    testWidgets('shows 100% confidence', (tester) async {
      await tester.pumpWidget(_wrap(const ConfidenceBar(confidence: 1.0)));
      expect(find.text('100%'), findsOneWidget);
    });

    testWidgets('renders progress indicator', (tester) async {
      await tester.pumpWidget(_wrap(const ConfidenceBar(confidence: 0.7)));
      expect(find.byType(LinearProgressIndicator), findsOneWidget);
    });
  });

  group('PnLBadge', () {
    testWidgets('shows positive value with + sign', (tester) async {
      await tester.pumpWidget(_wrap(const PnLBadge(value: 1250.50)));
      expect(find.textContaining('+'), findsWidgets);
      expect(find.textContaining('1250.50'), findsWidgets);
    });

    testWidgets('shows negative value without + sign', (tester) async {
      await tester.pumpWidget(_wrap(const PnLBadge(value: -300.0)));
      expect(find.textContaining('300.00'), findsWidgets);
    });

    testWidgets('shows percent when provided', (tester) async {
      await tester.pumpWidget(
          _wrap(const PnLBadge(value: 500, percent: 2.5)));
      expect(find.textContaining('2.50'), findsWidgets);
    });

    testWidgets('zero value shows no sign', (tester) async {
      await tester.pumpWidget(_wrap(const PnLBadge(value: 0)));
      expect(find.textContaining('0.00'), findsWidgets);
    });
  });

  group('StatCard', () {
    testWidgets('shows label and value', (tester) async {
      await tester.pumpWidget(_wrap(
        const StatCard(label: 'Open positions', value: '3'),
      ));
      expect(find.text('Open positions'), findsOneWidget);
      expect(find.text('3'), findsOneWidget);
    });

    testWidgets('shows icon when provided', (tester) async {
      await tester.pumpWidget(_wrap(
        const StatCard(
          label: 'Win rate',
          value: '71%',
          icon: Icons.emoji_events_outlined,
        ),
      ));
      expect(find.byIcon(Icons.emoji_events_outlined), findsOneWidget);
    });
  });

  group('ConnectionStatusBanner', () {
    testWidgets('is invisible when connected', (tester) async {
      await tester.pumpWidget(
          _wrap(const ConnectionStatusBanner(isConnected: true)));
      expect(find.textContaining('Disconnected'), findsNothing);
    });

    testWidgets('shows message when disconnected', (tester) async {
      await tester.pumpWidget(
          _wrap(const ConnectionStatusBanner(isConnected: false)));
      expect(find.textContaining('Disconnected'), findsOneWidget);
    });
  });

  group('LoadingCard', () {
    testWidgets('renders spinner', (tester) async {
      await tester.pumpWidget(_wrap(const LoadingCard()));
      expect(find.byType(CircularProgressIndicator), findsOneWidget);
    });

    testWidgets('uses custom height', (tester) async {
      await tester.pumpWidget(_wrap(const LoadingCard(height: 200)));
      final sizedBox = tester.widget<SizedBox>(
        find.ancestor(
          of: find.byType(CircularProgressIndicator),
          matching: find.byType(SizedBox),
        ).first,
      );
      expect(sizedBox.height, 200);
    });
  });
}
