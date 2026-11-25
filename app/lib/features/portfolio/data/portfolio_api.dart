import 'dart:convert';

import 'package:http/http.dart' as http;

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

/// Reads the paper portfolio from the demo proxy (GET /api/v1/portfolio/summary).
class PortfolioApi {
  final http.Client _http;
  final String _baseUrl;

  PortfolioApi({required http.Client httpClient, required String baseUrl})
      : _http = httpClient,
        _baseUrl = baseUrl;

  Future<PortfolioSnapshot> fetch() async {
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
}
