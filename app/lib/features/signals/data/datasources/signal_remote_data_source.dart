import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../domain/entities/signal_entity.dart';
import 'ws_events.dart';

class SignalRemoteDataSource {
  final http.Client _http;
  final String _baseUrl;
  final String _wsUrl;
  final String _wsToken;

  WebSocketChannel? _channel;
  final _streamController = StreamController<SignalEntity>.broadcast();
  final _eventController = StreamController<WsEvent>.broadcast();

  SignalRemoteDataSource({
    required http.Client httpClient,
    required String baseUrl,
    required String wsUrl,
    String wsToken = '',
  })  : _http = httpClient,
        _baseUrl = baseUrl,
        _wsUrl = wsUrl,
        _wsToken = wsToken;

  /// POST /api/v1/signals/generate
  Future<SignalEntity> generateSignal(String symbol, String strategy) async {
    final uri = Uri.parse('$_baseUrl/api/v1/signals/generate');
    final response = await _http
        .post(
          uri,
          headers: {'Content-Type': 'application/json'},
          body: jsonEncode({'symbol': symbol, 'strategy': strategy}),
        )
        .timeout(const Duration(seconds: 15));

    if (response.statusCode == 200) {
      return SignalEntity.fromJson(
        jsonDecode(response.body) as Map<String, dynamic>,
      );
    }
    throw HttpException(response.statusCode, response.body);
  }

  /// WebSocket stream from bot-engine /ws
  Stream<SignalEntity> watchSignals() {
    _ensureConnected();
    return _streamController.stream;
  }

  /// Every v1 event on /ws (intent.updated, order.filled, ...). Shares the
  /// connection with [watchSignals]; unknown types are left to the listener.
  Stream<WsEvent> watchEvents() {
    _ensureConnected();
    return _eventController.stream;
  }

  void _ensureConnected() {
    if (_channel == null) _connect();
  }

  void _connect() {
    if (_wsUrl.isEmpty) return; // live feed disabled (web demo)
    _channel?.sink.close();
    // BL-15: browsers cannot send an Authorization header on a WebSocket, so
    // the token travels as the second subprotocol ("bearer", token).
    _channel = WebSocketChannel.connect(
      Uri.parse(_wsUrl),
      protocols: _wsToken.isEmpty ? null : ['bearer', _wsToken],
    );
    _channel!.stream.listen(
      (data) {
        if (data is! String) return;
        final event = WsEvent.tryParse(data);
        if (event == null) return; // not a v1 envelope: ignore
        if (!_eventController.isClosed) _eventController.add(event);
        try {
          final signal = event.signal;
          if (signal != null) _streamController.add(signal);
        } catch (_) {} // malformed signal data: skip the message
      },
      onError: (_) => Future.delayed(
        const Duration(seconds: 3),
        _connect,
      ),
      onDone: () => Future.delayed(
        const Duration(seconds: 3),
        _connect,
      ),
    );
  }

  void dispose() {
    _channel?.sink.close();
    _streamController.close();
    _eventController.close();
  }
}

class HttpException implements Exception {
  final int statusCode;
  final String body;
  const HttpException(this.statusCode, this.body);
  @override
  String toString() => 'HttpException($statusCode): $body';
}
