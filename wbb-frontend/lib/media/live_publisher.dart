import 'dart:async';
import 'dart:math';

import 'package:flutter/widgets.dart';

import '../api/session_start_key.dart';
import '../api/wbb_api.dart';
import 'ice_servers.dart';
import 'media_platform.dart';

enum PublishPhase {
  idle,
  capturing,
  preparing,
  connecting,
  publishing,
  stopping,
  error,
}

class _Attempt {
  _Attempt(this.broadcastId, this.source, this.media, this.epoch);
  final String broadcastId;
  final PublishSource source;
  final PublisherMediaAttempt media;
  final int epoch;
  String? sessionId;
  WhipResource? resource;
  bool stopSent = false;
}

/// Owns one browser capture and its exact server session across screen dispose.
/// The UI sees phase/status only; grants and SDP stay inside this module.
class LivePublisher extends ChangeNotifier {
  LivePublisher(this._api, this._media);
  final ApiClient _api;
  final MediaPlatform _media;

  _Attempt? _active;
  String? _lastBroadcastId;
  PublishSource? _lastSource;
  Future<void>? _cleanup;
  int _attemptEpoch = 0;
  bool _disposed = false;
  PublishPhase phase = PublishPhase.idle;
  String? message;
  bool sharedAudio = true;
  bool audioRunning = true;

  bool get busy => _active != null || _cleanup != null;
  String? get broadcastId => _active?.broadcastId ?? _lastBroadcastId;
  PublishSource? get source => _active?.source ?? _lastSource;
  String? get sessionId => _active?.sessionId;
  Widget? get preview => _active?.media.preview;

  void _set(PublishPhase next, [String? detail]) {
    phase = next;
    message = detail;
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }

  Future<void> begin(
    String broadcastId, {
    PublishSource source = PublishSource.screen,
  }) {
    if (busy) return Future.value();
    final attempt = _Attempt(
      broadcastId,
      source,
      _media.newPublisher(source, () {
        unawaited(stop());
      }),
      ++_attemptEpoch,
    );
    _active = attempt;
    _lastBroadcastId = broadcastId;
    _lastSource = source;
    _set(PublishPhase.capturing);
    // This call enters getDisplayMedia before any API call or await.
    final capture = attempt.media.captureAndOffer();
    // Started after the device request so it never delays the user gesture.
    provideIceServers(_api.iceServersJson);
    return _run(attempt, capture);
  }

  Future<void> _run(_Attempt attempt, Future<CaptureOffer> capture) async {
    try {
      final offer = await capture;
      if (_active != attempt) {
        await attempt.media.stopLocal();
        return;
      }
      sharedAudio = offer.sharedAudio;
      audioRunning = offer.audioRunning;
      _set(
        PublishPhase.preparing,
        attempt.source == PublishSource.screen && !offer.sharedAudio
            ? '공유 소리가 없습니다. 소리를 포함한 탭을 선택해 주세요.'
            : null,
      );
      final prepared = await _api.prepareSession(
        attempt.broadcastId,
        source: attempt.source.name,
        idempotencyKey: createSessionStartKey(Random.secure()),
      );
      attempt.sessionId = prepared.session.id;
      if (_active != attempt) {
        await _stopServer(attempt);
        return;
      }
      _set(PublishPhase.connecting);
      final resource = await _api.postWhip(prepared, offer.sdp);
      attempt.resource = resource;
      if (_active != attempt) {
        await _stopServer(attempt);
        return;
      }
      await attempt.media.acceptAnswer(resource.answerSdp);
      if (_active != attempt) {
        await _stopServer(attempt);
        return;
      }
      _set(
        PublishPhase.publishing,
        offer.audioRunning ? null : '오디오가 일시 중지되었습니다. 이 화면에서 다시 시작해 주세요.',
      );
    } catch (e) {
      if (_active != attempt) return;
      final label = e is ApiException
          ? e.message
          : attempt.source == PublishSource.camera
          ? '카메라·마이크 권한 또는 송출 연결에 실패했습니다.'
          : '화면·마이크 또는 송출 연결에 실패했습니다.';
      await stop();
      if (attempt.epoch == _attemptEpoch) _set(PublishPhase.error, label);
    }
  }

  Future<void> _stopServer(_Attempt attempt) async {
    Object? error;
    final resource = attempt.resource;
    if (resource != null) {
      try {
        await _api.deleteWhip(resource);
      } catch (e) {
        error = e;
      }
      attempt.resource = null;
    }
    if (attempt.sessionId != null && !attempt.stopSent) {
      attempt.stopSent = true;
      try {
        await _api.cancelSession(attempt.broadcastId, attempt.sessionId!);
      } catch (e) {
        error ??= e;
      }
    }
    if (error != null && attempt.epoch == _attemptEpoch) {
      _set(PublishPhase.error, '서버 종료를 확인하지 못했습니다. 상태를 새로고침해 주세요.');
    }
  }

  Future<void> stop() {
    final attempt = _active;
    if (attempt == null) return _cleanup ?? Future.value();
    _active = null; // invalidate all later awaits first
    _set(PublishPhase.stopping);
    final localClose = attempt.media.stopLocal(); // tracks/PC close immediately
    final cleanup = () async {
      try {
        await localClose;
        await _stopServer(attempt);
        if (phase == PublishPhase.stopping) _set(PublishPhase.idle);
      } finally {
        _cleanup = null;
        if (!_disposed) notifyListeners();
      }
    }();
    _cleanup = cleanup;
    return cleanup;
  }

  Future<void> resumeAudio() async {
    final attempt = _active;
    if (attempt == null) return;
    try {
      final running = await attempt.media.resumeAudio();
      if (_active != attempt) return;
      audioRunning = running;
      _set(phase, running ? '오디오가 실행 중입니다.' : '오디오를 다시 시작하지 못했습니다.');
    } catch (_) {
      if (_active == attempt) _set(phase, '오디오를 다시 시작하지 못했습니다.');
    }
  }
}
