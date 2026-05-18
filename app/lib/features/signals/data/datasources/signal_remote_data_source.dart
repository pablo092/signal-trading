import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../domain/entities/signal_entity.dart';

class SignalRemoteDataSource {
  final http.Client _http;
  final String _baseUrl;
  final String _wsUrl;

  WebSocketChannel? _channel;
  final _streamController = StreamController<SignalEntity>.broadcast();

  SignalRemoteDataSource({
    required http.Client httpClient,
    required String baseUrl,
    required String wsUrl,
  })  : _http = httpClient,
        _baseUrl = baseUrl,
        _wsUrl = wsUrl;

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
    _connect();
    return _streamController.stream;
  }

  void _connect() {
    _channel?.sink.close();
    _channel = WebSocketChannel.connect(Uri.parse(_wsUrl));
    _channel!.stream.listen(
      (data) {
        try {
          final json = jsonDecode(data as String) as Map<String, dynamic>;
          if (json['type'] == 'signal') {
            _streamController.add(SignalEntity.fromJson(
              json['data'] as Map<String, dynamic>,
            ));
          }
        } catch (_) {}
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
  }
}

class HttpException implements Exception {
  final int statusCode;
  final String body;
  const HttpException(this.statusCode, this.body);
  @override
  String toString() => 'HttpException($statusCode): $body';
}
