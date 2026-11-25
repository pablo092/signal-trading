import 'dart:async';

import 'package:bloc/bloc.dart';
import 'package:dartz/dartz.dart' show Either;
import 'package:equatable/equatable.dart';

import '../../../../core/errors/failures.dart';
import '../../../../core/network/idempotency.dart';
import '../../domain/entities/order_intent.dart';
import '../../domain/repositories/i_order_intent_repository.dart';

// ══════════════════════════════════════════════
// Events
// ══════════════════════════════════════════════

abstract class IntentsEvent extends Equatable {
  const IntentsEvent();
  @override
  List<Object?> get props => [];
}

/// Reload the recent and the pending-approval lists.
class IntentsRequested extends IntentsEvent {
  const IntentsRequested();
}

/// "Propose trade": create an intent; the engine fetches the signal and sizes it.
class IntentProposed extends IntentsEvent {
  final String symbol;
  final String strategy;
  const IntentProposed({required this.symbol, required this.strategy});
  @override
  List<Object?> get props => [symbol, strategy];
}

class IntentApproved extends IntentsEvent {
  final String id;
  final String? reason;
  const IntentApproved(this.id, {this.reason});
  @override
  List<Object?> get props => [id, reason];
}

class IntentRejected extends IntentsEvent {
  final String id;
  final String reason;
  const IntentRejected(this.id, {required this.reason});
  @override
  List<Object?> get props => [id, reason];
}

class IntentCanceled extends IntentsEvent {
  final String id;
  const IntentCanceled(this.id);
  @override
  List<Object?> get props => [id];
}

/// Follow intent.updated / order.filled on the WebSocket.
class IntentsWatchStarted extends IntentsEvent {
  const IntentsWatchStarted();
}

class _IntentPushed extends IntentsEvent {
  final IntentLiveUpdate update;
  const _IntentPushed(this.update);
  @override
  List<Object?> get props => [update.intent];
}

// ══════════════════════════════════════════════
// State
// ══════════════════════════════════════════════

enum IntentsLoad { idle, loading, ready, failure }

/// A one-shot message for a SnackBar; [seq] changes on every notice.
class IntentNotice extends Equatable {
  final int seq;
  final String message;
  final bool isError;

  /// Error code and request id, e.g. "intent_expired · request abc".
  final String? reference;

  const IntentNotice({
    required this.seq,
    required this.message,
    this.isError = false,
    this.reference,
  });

  @override
  List<Object?> get props => [seq, message, isError, reference];
}

class IntentsState extends Equatable {
  final IntentsLoad load;

  /// Newest first, every status.
  final List<OrderIntent> recent;

  /// Intents waiting for a human decision.
  final List<OrderIntent> pending;
  final Failure? loadFailure;
  final bool proposing;

  /// The result of the last "Propose trade".
  final OrderIntent? lastProposed;

  /// Intent ids with an approve / reject / cancel in flight.
  final Set<String> busyIds;
  final IntentNotice? notice;

  const IntentsState({
    this.load = IntentsLoad.idle,
    this.recent = const [],
    this.pending = const [],
    this.loadFailure,
    this.proposing = false,
    this.lastProposed,
    this.busyIds = const {},
    this.notice,
  });

  IntentsState copyWith({
    IntentsLoad? load,
    List<OrderIntent>? recent,
    List<OrderIntent>? pending,
    Failure? loadFailure,
    bool clearLoadFailure = false,
    bool? proposing,
    OrderIntent? lastProposed,
    Set<String>? busyIds,
    IntentNotice? notice,
  }) {
    return IntentsState(
      load: load ?? this.load,
      recent: recent ?? this.recent,
      pending: pending ?? this.pending,
      loadFailure: clearLoadFailure ? null : (loadFailure ?? this.loadFailure),
      proposing: proposing ?? this.proposing,
      lastProposed: lastProposed ?? this.lastProposed,
      busyIds: busyIds ?? this.busyIds,
      notice: notice ?? this.notice,
    );
  }

  @override
  List<Object?> get props => [
        load,
        recent,
        pending,
        loadFailure,
        proposing,
        lastProposed,
        busyIds,
        notice,
      ];
}

// ══════════════════════════════════════════════
// Messages
// ══════════════════════════════════════════════

/// A human-readable explanation of a failed intent call.
String intentFailureMessage(Failure failure) {
  if (failure is TimeoutFailure) {
    return 'The request timed out. The outcome is unknown: check the intent '
        'list before retrying.';
  }
  if (failure is NetworkFailure) return 'Network error: ${failure.message}';
  if (failure is! ApiFailure) return failure.message;
  if (failure.statusCode == 401) {
    return 'bot-engine rejected the token. Check BOT_ENGINE_TOKEN.';
  }
  return switch (failure.code) {
    'engine_token_missing' => failure.message,
    'execution_disabled' => 'Order execution is disabled on this engine '
        '(ORDER_EXECUTION_ENABLED=false). Nothing was sent to the broker; '
        'the intent stays pending until it expires.',
    'insufficient_scope' => 'This token lacks the '
        '${failure.requiredScope ?? 'required'} scope. Approving or rejecting '
        'needs a human token with intent:approve.',
    'intent_not_pending' => 'This intent is no longer pending approval: it '
        'was already decided, canceled or changed.',
    'intent_expired' => 'The approval window has passed and the intent '
        'expired. Propose the trade again if it still makes sense.',
    'kill_switch_engaged' => 'The kill switch is engaged: no intent can be '
        'created or approved.',
    'intent_exists' =>
      'An active intent already exists for this signal. ${failure.message}',
    'rate_limited' => 'Too many proposals this hour. Wait before proposing '
        'again.',
    'not_intent_owner' => 'Only the actor that created this intent can '
        'cancel it.',
    'signal_unavailable' => 'signal-service did not return a signal. Try '
        'again shortly.',
    'idempotency_key_reused' => 'That request key was already used for a '
        'different request. Try again.',
    _ => failure.statusCode == 429
        ? 'Too many requests. Wait before trying again.'
        : failure.message,
  };
}

/// A failure after which re-sending the same `Idempotency-Key` is the safe
/// retry: the engine may have stored the intent.
bool _replaySafe(Failure f) =>
    f is TimeoutFailure ||
    f is NetworkFailure ||
    (f is ApiFailure && f.statusCode >= 500);

// ══════════════════════════════════════════════
// BLoC
// ══════════════════════════════════════════════

/// IntentsBloc — order intents: list, propose, human approval, live updates.
/// Only created when an engine token is configured (see AppConfig.engineEnabled).
class IntentsBloc extends Bloc<IntentsEvent, IntentsState> {
  final IOrderIntentRepository _repository;

  StreamSubscription<IntentLiveUpdate>? _subscription;
  int _seq = 0;

  // Reused for a retry of the same proposal after an unknown outcome.
  String? _proposeKey;
  String? _proposeTarget;

  static const int _recentLimit = 50;
  static const int _pendingLimit = 200;

  IntentsBloc({required IOrderIntentRepository repository})
      : _repository = repository,
        super(const IntentsState()) {
    on<IntentsRequested>(_onRequested);
    on<IntentProposed>(_onProposed);
    on<IntentApproved>(_onApproved);
    on<IntentRejected>(_onRejected);
    on<IntentCanceled>(_onCanceled);
    on<IntentsWatchStarted>(_onWatchStarted);
    on<_IntentPushed>(_onPushed);
  }

  IntentNotice _notice(String message, {Failure? failure}) => IntentNotice(
        seq: ++_seq,
        message: message,
        isError: failure != null,
        reference: failure is ApiFailure ? failure.reference : null,
      );

  Future<void> _onRequested(
    IntentsRequested event,
    Emitter<IntentsState> emit,
  ) async {
    emit(state.copyWith(load: IntentsLoad.loading));
    final results = await Future.wait([
      _repository.list(limit: _recentLimit),
      _repository.list(
        status: IntentStatus.pendingApproval,
        limit: _pendingLimit,
      ),
    ]);
    final recent = results[0];
    final pending = results[1];
    final failure = recent.fold<Failure?>((f) => f, (_) => null) ??
        pending.fold<Failure?>((f) => f, (_) => null);
    if (failure != null) {
      emit(state.copyWith(load: IntentsLoad.failure, loadFailure: failure));
      return;
    }
    emit(state.copyWith(
      load: IntentsLoad.ready,
      recent: recent.getOrElse(() => const []),
      pending: pending.getOrElse(() => const []),
      clearLoadFailure: true,
    ));
  }

  Future<void> _onProposed(
    IntentProposed event,
    Emitter<IntentsState> emit,
  ) async {
    final target = '${event.symbol}|${event.strategy}';
    if (_proposeKey == null || _proposeTarget != target) {
      _proposeKey = newIdempotencyKey();
      _proposeTarget = target;
    }
    emit(state.copyWith(proposing: true));
    final result = await _repository.create(
      symbol: event.symbol,
      strategy: event.strategy,
      idempotencyKey: _proposeKey!,
    );
    result.fold(
      (failure) {
        if (!_replaySafe(failure)) _proposeKey = null;
        emit(state.copyWith(
          proposing: false,
          notice: _notice(intentFailureMessage(failure), failure: failure),
        ));
      },
      (intent) {
        _proposeKey = null;
        final message = intent.status == IntentStatus.rejected
            ? 'Proposal rejected by the risk checks.'
            : intent.isPendingApproval
                ? 'Proposed: waiting for a human approval.'
                : 'Proposal recorded (${intent.status.label}).';
        emit(_applied(intent).copyWith(
          proposing: false,
          lastProposed: intent,
          notice: _notice(message),
        ));
      },
    );
  }

  Future<void> _onApproved(
    IntentApproved event,
    Emitter<IntentsState> emit,
  ) async {
    emit(state.copyWith(busyIds: {...state.busyIds, event.id}));
    final result = await _repository.approve(event.id, reason: event.reason);
    final busy = {...state.busyIds}..remove(event.id);
    result.fold(
      (failure) {
        emit(state.copyWith(
          busyIds: busy,
          notice: _notice(intentFailureMessage(failure), failure: failure),
        ));
        if ((failure is ApiFailure && failure.statusCode == 409) ||
            failure is TimeoutFailure) {
          add(const IntentsRequested());
        }
      },
      (r) {
        final intent = r.intent;
        final String message;
        if (r.outcomeUnknown) {
          message = 'Approved, but the broker outcome is unknown. Do not '
              'retry: the reconciler will resolve it.';
        } else if (intent.status == IntentStatus.rejected) {
          message = 'Rejected at submission'
              '${intent.rejectionCode == null ? '' : ' (${intent.rejectionCode})'}.';
        } else {
          message = 'Approved: ${intent.status.label}.';
        }
        emit(_applied(intent).copyWith(
          busyIds: busy,
          notice: _notice(message),
        ));
      },
    );
  }

  Future<void> _onRejected(
    IntentRejected event,
    Emitter<IntentsState> emit,
  ) async {
    emit(state.copyWith(busyIds: {...state.busyIds, event.id}));
    final result = await _repository.reject(event.id, reason: event.reason);
    _emitDecision(result, event.id, 'Intent rejected.', emit);
  }

  Future<void> _onCanceled(
    IntentCanceled event,
    Emitter<IntentsState> emit,
  ) async {
    emit(state.copyWith(busyIds: {...state.busyIds, event.id}));
    final result = await _repository.cancel(event.id);
    _emitDecision(result, event.id, 'Intent canceled.', emit);
  }

  void _emitDecision(
    Either<Failure, OrderIntent> result,
    String id,
    String okMessage,
    Emitter<IntentsState> emit,
  ) {
    final busy = {...state.busyIds}..remove(id);
    result.fold(
      (failure) {
        emit(state.copyWith(
          busyIds: busy,
          notice: _notice(intentFailureMessage(failure), failure: failure),
        ));
        if (failure is ApiFailure && failure.statusCode == 409) {
          add(const IntentsRequested());
        }
      },
      (intent) {
        emit(_applied(intent).copyWith(
          busyIds: busy,
          notice: _notice(okMessage),
        ));
      },
    );
  }

  void _onWatchStarted(
    IntentsWatchStarted event,
    Emitter<IntentsState> emit,
  ) {
    _subscription?.cancel();
    _subscription = _repository.watchUpdates().listen(
          (u) => add(_IntentPushed(u)),
          onError: (Object _) {}, // the WebSocket reconnects on its own
        );
  }

  void _onPushed(_IntentPushed event, Emitter<IntentsState> emit) {
    final intent = event.update.intent;
    if (intent == null) {
      add(const IntentsRequested()); // order.filled: reload
      return;
    }
    emit(_applied(intent));
  }

  /// [state] with [intent] merged into both lists (stale updates ignored).
  IntentsState _applied(OrderIntent intent) {
    final last = state.lastProposed;
    final known = [...state.recent, ...state.pending, if (last != null) last];
    final stale = known.any(
        (i) => i.id == intent.id && i.updatedAt.isAfter(intent.updatedAt));
    if (stale) return state;
    return state.copyWith(
      recent: _upsert(state.recent, intent),
      pending: intent.isPendingApproval
          ? _upsert(state.pending, intent)
          : state.pending.where((i) => i.id != intent.id).toList(),
      lastProposed: last != null && last.id == intent.id ? intent : null,
    );
  }

  static List<OrderIntent> _upsert(List<OrderIntent> list, OrderIntent intent) {
    final index = list.indexWhere((i) => i.id == intent.id);
    if (index < 0) return [intent, ...list];
    if (list[index].updatedAt.isAfter(intent.updatedAt)) return list;
    final copy = List<OrderIntent>.of(list);
    copy[index] = intent;
    return copy;
  }

  @override
  Future<void> close() {
    _subscription?.cancel();
    return super.close();
  }
}
