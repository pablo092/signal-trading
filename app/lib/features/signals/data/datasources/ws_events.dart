import 'dart:convert';

import '../../../orders/domain/entities/order_intent.dart';
import '../../domain/entities/signal_entity.dart';

/// WebSocket event envelope version this app understands
/// (contracts/schemas/ws-event.schema.json).
const int wsEventVersion = 1;

/// Event types sent by bot-engine on /ws.
const String wsTypeSignalReceived = 'signal.received';
const String wsTypeOrderPlaced = 'order.placed';
const String wsTypeKillSwitchChanged = 'kill_switch.changed';
const String wsTypeIntentUpdated = 'intent.updated';
const String wsTypeOrderFilled = 'order.filled';

/// A decoded WebSocket event.
class WsEvent {
  final int version;
  final String type;
  final String id;
  final DateTime ts;
  final Map<String, dynamic> data;

  const WsEvent({
    required this.version,
    required this.type,
    required this.id,
    required this.ts,
    required this.data,
  });

  /// Returns null for anything that is not a v1 envelope, so unknown or
  /// malformed messages are ignored instead of breaking the stream.
  static WsEvent? tryParse(String raw) {
    try {
      final json = jsonDecode(raw);
      if (json is! Map<String, dynamic>) return null;
      if (json['v'] != wsEventVersion) return null;
      final data = json['data'];
      if (data is! Map<String, dynamic>) return null;
      return WsEvent(
        version: json['v'] as int,
        type: json['type'] as String,
        id: json['id'] as String,
        ts: DateTime.parse(json['ts'] as String),
        data: data,
      );
    } catch (_) {
      return null;
    }
  }

  /// The signal carried by a signal.received event, or null.
  SignalEntity? get signal =>
      type == wsTypeSignalReceived ? SignalEntity.fromJson(data) : null;

  /// The intent carried by an intent.updated event, or null (also when the
  /// data does not decode, so one bad message cannot break the stream).
  OrderIntent? get intent {
    if (type != wsTypeIntentUpdated) return null;
    try {
      return OrderIntent.fromJson(data);
    } catch (_) {
      return null;
    }
  }
}
