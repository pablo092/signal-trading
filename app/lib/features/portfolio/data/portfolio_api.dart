import 'dart:convert';

import 'package:http/http.dart' as http;

import '../../../core/network/engine_http.dart';

class PositionItem {
  final String symbol;
  final double quantity;
  final double averageEntryPrice;
  final double currentPrice;
  final double unrealizedPnl;

  const PositionItem({
    required this.symbol,
    required this.quantity,
    required this.averageEntryPrice,
    required this.currentPrice,
    required this.unrealizedPnl,
  });

  factory PositionItem.fromJson(Map<String, dynamic> json) => PositionItem(
        symbol: json['symbol'] as String? ?? '',
        quantity: (json['quantity'] as num? ?? 0).toDouble(),
        averageEntryPrice: (json['average_entry_price'] as num? ?? 0).toDouble(),
        currentPrice: (json['current_price'] as num? ?? 0).toDouble(),
        unrealizedPnl: (json['unrealized_pnl'] as num? ?? 0).toDouble(),
      );

  /// GET /api/v1/portfolio/positions (portfolio-positions.schema.json).
  static List<PositionItem> listFromJson(List<dynamic> json) => json
      .whereType<Map<String, dynamic>>()
      .map(PositionItem.fromJson)
      .toList();
}

/// GET /api/v1/portfolio/equity (portfolio-equity.schema.json).
class PortfolioEquity {
  final double equity;
  final bool paperMode;
  final bool marketOpen;

  const PortfolioEquity({
    required this.equity,
    required this.paperMode,
    required this.marketOpen,
  });

  factory PortfolioEquity.fromJson(Map<String, dynamic> json) => PortfolioEquity(
        equity: (json['equity'] as num).toDouble(),
        paperMode: json['paper_mode'] as bool,
        marketOpen: json['market_open'] as bool,
      );
}

class PortfolioSnapshot {
  final double equity;
  final bool marketOpen;
  final bool paperMode;
  final bool orderExecutionEnabled;
  final List<PositionItem> positions;

  const PortfolioSnapshot({
    required this.equity,
    required this.marketOpen,
    required this.paperMode,
    required this.orderExecutionEnabled,
    required this.positions,
  });

  double get unrealizedPnl =>
      positions.fold(0.0, (sum, p) => sum + p.unrealizedPnl);

  factory PortfolioSnapshot.fromParts({
    required PortfolioEquity equity,
    required List<PositionItem> positions,
    bool orderExecutionEnabled = false,
  }) =>
      PortfolioSnapshot(
        equity: equity.equity,
        marketOpen: equity.marketOpen,
        paperMode: equity.paperMode,
        orderExecutionEnabled: orderExecutionEnabled,
        positions: positions,
      );

  factory PortfolioSnapshot.fromJson(Map<String, dynamic> json) {
    final raw = json['positions'];
    final items = raw is List ? raw : const [];
    return PortfolioSnapshot(
      equity: (json['equity'] as num? ?? 0).toDouble(),
      marketOpen: json['market_open'] as bool? ?? false,
      paperMode: json['paper_mode'] as bool? ?? true,
      orderExecutionEnabled: json['order_execution_enabled'] as bool? ?? false,
      positions: items
          .whereType<Map<String, dynamic>>()
          .map(PositionItem.fromJson)
          .toList(),
    );
  }
}

/// Reads the paper portfolio.
///
/// - With an engine token (local builds): GET /api/v1/portfolio/equity and
///   /positions on bot-engine directly, with `Authorization: Bearer`.
/// - Without one on the web demo: the read-only same-origin proxy
///   (GET /api/v1/portfolio/summary), which holds its token server-side.
/// - Otherwise: unavailable; no request is sent.
class PortfolioApi {
  final http.Client _http;
  final String _baseUrl;
  final EngineHttp? _engine;
  final bool _useDemoProxy;

  PortfolioApi({
    required http.Client httpClient,
    required String baseUrl,
    EngineHttp? engine,
    bool useDemoProxy = false,
  })  : _http = httpClient,
        _baseUrl = baseUrl,
        _engine = engine,
        _useDemoProxy = useDemoProxy;

  bool get _direct => _engine?.enabled ?? false;

  /// False means the UI must show the "connect a token" state.
  bool get available => _direct || _useDemoProxy;

  Future<PortfolioSnapshot> fetch() async {
    final engine = _engine;
    if (engine != null && engine.enabled) return _fetchDirect(engine);
    if (!_useDemoProxy) throw const ApiException(engineTokenMissing);
    final response = await _http
        .get(Uri.parse('$_baseUrl/api/v1/portfolio/summary'))
        .timeout(const Duration(seconds: 20));
    if (response.statusCode != 200) {
      throw Exception('Portfolio unavailable (HTTP ${response.statusCode})');
    }
    return PortfolioSnapshot.fromJson(
      jsonDecode(response.body) as Map<String, dynamic>,
    );
  }

  Future<PortfolioSnapshot> _fetchDirect(EngineHttp engine) async {
    final responses = await Future.wait([
      engine.get('/api/v1/portfolio/equity'),
      engine.get('/api/v1/portfolio/positions'),
    ]);
    final equity = PortfolioEquity.fromJson(
        EngineHttp.decode(responses[0]) as Map<String, dynamic>);
    final positions =
        PositionItem.listFromJson(EngineHttp.decode(responses[1]) as List);
    var executionEnabled = false;
    try {
      final mode = EngineHttp.decode(await engine.get('/api/v1/trading-mode'))
          as Map<String, dynamic>;
      executionEnabled = mode['order_execution_enabled'] as bool? ?? false;
    } catch (_) {} // informational only
    return PortfolioSnapshot.fromParts(
      equity: equity,
      positions: positions,
      orderExecutionEnabled: executionEnabled,
    );
  }
}
