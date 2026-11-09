import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../models/broadcast.dart';
import '../models/live_session.dart';
import '../media/live_publisher.dart';
import '../media/media_platform.dart';
import '../state/auth_controller.dart';

/// Public detail, owner capture controls and anonymous playback.
class BroadcastDetailScreen extends StatefulWidget {
  const BroadcastDetailScreen({
    super.key,
    required this.broadcastId,
    this.publisher,
    this.mediaPlatform,
  });
  final String broadcastId;
  final LivePublisher? publisher;
  final MediaPlatform? mediaPlatform;
  @override
  State<BroadcastDetailScreen> createState() => _BroadcastDetailScreenState();
}

class _BroadcastDetailScreenState extends State<BroadcastDetailScreen> {
  Broadcast? _broadcast;
  LiveSession? _session;
  bool _loading = true;
  int _loadEpoch = 0;
  int? _fetchingEpoch;
  bool get _fetching => _fetchingEpoch != null;
  bool _acting = false;
  bool _canceling = false;
  int _actionEpoch = 0;
  String? _error;
  String? _actionError;
  Timer? _poll;
  LivePublisher? _publisher;
  ViewerMediaPlayer? _player;
  String? _playerSessionId;
  String? _playbackTargetSessionId;
  String _playbackStatus = '방송 준비 중';
  int? _checkingPlaybackEpoch;
  int _playbackEpoch = 0;

  @override
  void initState() {
    super.initState();
    _load();
    _poll = Timer.periodic(
      const Duration(seconds: 5),
      (_) => _load(silent: true),
    );
  }

  @override
  void dispose() {
    _poll?.cancel();
    _loadEpoch++;
    _playbackEpoch++;
    _clearPlayer();
    if (_publisher?.broadcastId == widget.broadcastId) {
      unawaited(_publisher!.stop());
    }
    super.dispose();
  }

  LivePublisher get _livePublisher =>
      _publisher ??= widget.publisher ?? context.read<LivePublisher>();

  void _clearPlayer() {
    _player?.dispose();
    _player = null;
    _playerSessionId = null;
  }

  Future<void> _checkPlayback(LiveSession? session) async {
    if (session?.state != 'live') {
      _playbackEpoch++;
      _checkingPlaybackEpoch = null;
      _playbackTargetSessionId = null;
      _clearPlayer();
      if (mounted) {
        setState(() {
          _playbackStatus = '방송 준비 중';
        });
      }
      return;
    }
    if (_playbackTargetSessionId != session!.id) {
      _playbackTargetSessionId = session.id;
      _playbackEpoch++;
      _checkingPlaybackEpoch = null;
      _clearPlayer();
      if (mounted) {
        setState(() {
          _playbackStatus = '새 방송 영상 준비 중입니다.';
        });
      }
    }
    if (_playerSessionId == session.id ||
        _checkingPlaybackEpoch == _playbackEpoch) {
      return;
    }
    final epoch = ++_playbackEpoch;
    _checkingPlaybackEpoch = epoch;
    try {
      final access = await context.read<ApiClient>().playback(
        widget.broadcastId,
      );
      if (!mounted ||
          epoch != _playbackEpoch ||
          _session?.id != access.sessionId ||
          _session?.state != 'live') {
        return;
      }
      final media = widget.mediaPlatform ?? context.read<MediaPlatform>();
      final next = media.newViewer(access.masterUrl, (event) {
        if (!mounted || epoch != _playbackEpoch) return;
        setState(() {
          _playbackStatus = event == 'autoplay-blocked'
              ? '자동재생이 차단되었습니다. 재생을 눌러 주세요.'
              : '재생 오류가 발생했습니다. 다시 시도해 주세요.';
        });
      });
      setState(() {
        _clearPlayer();
        _player = next;
        _playerSessionId = access.sessionId;
        _playbackStatus = '재생할 수 있습니다.';
      });
    } on ApiException catch (e) {
      if (!mounted || epoch != _playbackEpoch) return;
      setState(() {
        _playbackStatus = e.status == 409
            ? '영상 준비 중입니다. 잠시 후 다시 시도해 주세요.'
            : e.message;
      });
    } finally {
      if (_checkingPlaybackEpoch == epoch) _checkingPlaybackEpoch = null;
    }
  }

  Future<void> _play({required bool withSound}) async {
    final player = _player;
    if (player == null) return;
    try {
      await player.play(withSound: withSound);
      if (mounted && identical(player, _player)) {
        setState(() {
          _playbackStatus = '재생 중';
        });
      }
    } catch (_) {
      if (mounted && identical(player, _player)) {
        setState(() {
          _playbackStatus = '재생이 차단되거나 실패했습니다. 다시 눌러 주세요.';
        });
      }
    }
  }

  Future<void> _resumeAudio() async {
    await _livePublisher.resumeAudio();
  }

  Future<void> _load({bool silent = false, bool force = false}) async {
    if (_fetching && !force) return;
    final epoch = ++_loadEpoch;
    _fetchingEpoch = epoch;
    if (!silent) {
      setState(() {
        _loading = true;
        _error = null;
      });
    }
    try {
      final api = context.read<ApiClient>();
      final b = await api.getBroadcast(widget.broadcastId);
      final session = await api.currentSession(widget.broadcastId);
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _broadcast = b;
        _session = session;
        _loading = false;
        _error = null;
      });
      if (_publisher?.broadcastId == widget.broadcastId &&
          _publisher?.sessionId != null &&
          (_publisher?.sessionId != session?.id ||
              session?.state == 'stopping' ||
              session?.state == 'ended')) {
        unawaited(_publisher!.stop());
      }
      unawaited(_checkPlayback(session));
    } on ApiException catch (e) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _error = e.status == 404 ? '방송을 찾을 수 없습니다.' : e.message;
        _loading = false;
      });
    } finally {
      if (_fetchingEpoch == epoch) {
        _fetchingEpoch = null;
        if (mounted) setState(() {});
      }
    }
  }

  Future<void> _prepare() async {
    if (_acting) return;
    final epoch = ++_actionEpoch;
    setState(() {
      _acting = true;
      _actionError = null;
    });
    try {
      // LivePublisher invokes getDisplayMedia before its first await/API call.
      await _livePublisher.begin(widget.broadcastId);
      if (!mounted || epoch != _actionEpoch) return;
      await _load(silent: true, force: true);
    } on ApiException catch (e) {
      if (!mounted || epoch != _actionEpoch) return;
      setState(() {
        _actionError = e.message;
      });
      if (e.status == 409) await _load(silent: true, force: true);
    } finally {
      if (mounted && epoch == _actionEpoch) {
        setState(() {
          _acting = false;
        });
      }
    }
  }

  Future<void> _cancel() async {
    if (_canceling || (_session == null && !(_publisher?.busy ?? false))) {
      return;
    }
    ++_actionEpoch;
    setState(() {
      _acting = false;
      _canceling = true;
      _actionError = null;
    });
    try {
      LiveSession? session;
      if (_publisher?.broadcastId == widget.broadcastId && _publisher!.busy) {
        await _publisher!.stop();
      } else if (_session != null) {
        session = await context.read<ApiClient>().cancelSession(
          widget.broadcastId,
          _session!.id,
        );
      }
      if (!mounted) return;
      setState(() {
        if (session != null) _session = session;
      });
      await _load(silent: true, force: true);
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _actionError = e.message;
      });
      await _load(silent: true, force: true);
    } finally {
      if (mounted) {
        setState(() {
          _canceling = false;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final b = _broadcast;
    final owner =
        b != null && context.watch<AuthController>().user?.id == b.ownerId;
    final state = _session?.state ?? b?.status ?? 'ready';
    final publisher = owner
        ? (widget.publisher ?? context.watch<LivePublisher>())
        : null;
    if (owner) _publisher ??= publisher;
    return Scaffold(
      appBar: AppBar(
        title: Text(b?.title ?? '방송'),
        actions: [
          IconButton(
            key: const Key('detail-refresh'),
            tooltip: '상태 새로고침',
            onPressed: _fetching ? null : () => _load(),
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
          ? Center(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(_error!),
                  const SizedBox(height: 12),
                  OutlinedButton(
                    key: const Key('detail-retry'),
                    onPressed: _load,
                    child: const Text('다시 시도'),
                  ),
                ],
              ),
            )
          : b == null
          ? const Center(child: Text('방송 정보가 없습니다.'))
          : ListView(
              padding: const EdgeInsets.all(16),
              children: [
                Text(b.title, style: Theme.of(context).textTheme.headlineSmall),
                const SizedBox(height: 8),
                Text('방송자: ${b.ownerDisplayName}'),
                const SizedBox(height: 4),
                Text(
                  '상태: ${_statusLabel(state)}',
                  key: const Key('session-state'),
                ),
                const SizedBox(height: 4),
                Text('방송 등록: ${b.createdAt.toLocal()}'),
                if (b.description.isNotEmpty) ...[
                  const SizedBox(height: 16),
                  Text(b.description),
                ],
                const SizedBox(height: 20),
                if (state == 'starting')
                  const Text('회차를 준비 중입니다. 영상 준비 전에는 시청할 수 없습니다.'),
                if (state == 'stopping')
                  const Text('종료 처리 중입니다. 잠시 후 상태를 새로고침해 주세요.'),
                if (owner && (state == 'ready' || state == 'ended')) ...[
                  FilledButton(
                    key: const Key('session-prepare'),
                    onPressed: _acting || publisher!.busy ? null : _prepare,
                    child: const Text('화면 공유 시작'),
                  ),
                ],
                if (owner &&
                    (publisher!.busy || state == 'starting' || state == 'live'))
                  OutlinedButton(
                    key: const Key('session-cancel'),
                    onPressed: _canceling ? null : _cancel,
                    child: const Text('방송 중지'),
                  ),
                if (owner && publisher!.broadcastId == widget.broadcastId) ...[
                  Text(
                    '송출: ${publisher.phase.name}',
                    key: const Key('publish-phase'),
                  ),
                  if (publisher.message != null) Text(publisher.message!),
                  if (!publisher.sharedAudio)
                    const Text('공유 소리가 없습니다. 소리를 포함한 탭을 다시 선택해 주세요.'),
                  if (!publisher.audioRunning && publisher.busy)
                    OutlinedButton(
                      key: const Key('publish-resume-audio'),
                      onPressed: _resumeAudio,
                      child: const Text('오디오 다시 시작'),
                    ),
                  if (publisher.audioRunning &&
                      publisher.phase == PublishPhase.publishing)
                    const Text('오디오 실행 중', key: Key('publish-audio-running')),
                  if (publisher.preview != null)
                    SizedBox(height: 240, child: publisher.preview),
                ],
                const SizedBox(height: 16),
                Text(_playbackStatus, key: const Key('playback-status')),
                if (_player != null)
                  SizedBox(height: 320, child: _player!.view),
                Wrap(
                  spacing: 8,
                  children: [
                    FilledButton.icon(
                      key: const Key('playback-play'),
                      onPressed: _player == null
                          ? null
                          : () => _play(withSound: false),
                      icon: const Icon(Icons.play_arrow),
                      label: const Text('재생'),
                    ),
                    OutlinedButton(
                      key: const Key('playback-sound'),
                      onPressed: _player == null
                          ? null
                          : () => _play(withSound: true),
                      child: const Text('소리 켜기'),
                    ),
                    OutlinedButton(
                      key: const Key('playback-live'),
                      onPressed: _player == null
                          ? null
                          : () => _player?.goLive(),
                      child: const Text('라이브로 이동'),
                    ),
                    OutlinedButton(
                      key: const Key('playback-retry'),
                      onPressed: state != 'live'
                          ? null
                          : () {
                              _clearPlayer();
                              _checkPlayback(_session);
                            },
                      child: const Text('재시도'),
                    ),
                  ],
                ),
                if (_actionError != null)
                  Text(_actionError!, key: const Key('session-action-error')),
              ],
            ),
    );
  }

  String _statusLabel(String state) => switch (state) {
    'starting' => '준비 중',
    'stopping' => '종료 처리 중',
    'ended' => '종료',
    'live' => '방송 중',
    _ => '대기',
  };
}
