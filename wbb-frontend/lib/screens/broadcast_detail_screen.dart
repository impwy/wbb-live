import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../models/broadcast.dart';
import '../models/live_session.dart';
import '../media/live_publisher.dart';
import '../media/media_platform.dart';
import '../state/auth_controller.dart';
import 'broadcast_form_screen.dart';
import 'home_screen.dart' show BroadcastStatusBadge, formatBroadcastDate;

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

class _BroadcastDetailScreenState extends State<BroadcastDetailScreen>
    with WidgetsBindingObserver {
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
    WidgetsBinding.instance.addObserver(this);
    _load();
    _poll = Timer.periodic(
      const Duration(seconds: 5),
      (_) => _load(silent: true),
    );
  }

  @override
  void dispose() {
    _publisher?.removeListener(_onPublisherChanged);
    WidgetsBinding.instance.removeObserver(this);
    _poll?.cancel();
    _loadEpoch++;
    _playbackEpoch++;
    _clearPlayer();
    if (_publisher?.broadcastId == widget.broadcastId) {
      unawaited(_publisher!.stop());
    }
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.hidden ||
        state == AppLifecycleState.paused) {
      if (_publisher?.broadcastId == widget.broadcastId &&
          _publisher?.source == PublishSource.camera) {
        _invalidatePendingCameraAction();
        unawaited(_publisher!.stop());
      }
    } else if (state == AppLifecycleState.resumed && mounted) {
      unawaited(_load(silent: true, force: true));
    }
  }

  void _invalidatePendingCameraAction() {
    if (!_acting) return;
    ++_actionEpoch;
    if (mounted) setState(() => _acting = false);
  }

  void _onPublisherChanged() {
    final publisher = _publisher;
    if (publisher?.broadcastId != widget.broadcastId ||
        publisher?.source != PublishSource.camera) {
      return;
    }
    // JS can report visibility/pagehide before Flutter receives a lifecycle
    // event. A pending permission Future must not keep the controls locked.
    if (publisher!.phase == PublishPhase.stopping ||
        publisher.phase == PublishPhase.idle ||
        publisher.phase == PublishPhase.error) {
      _invalidatePendingCameraAction();
    }
  }

  void _bindPublisher(LivePublisher publisher) {
    if (identical(_publisher, publisher)) return;
    _publisher?.removeListener(_onPublisherChanged);
    _publisher = publisher;
    publisher.addListener(_onPublisherChanged);
  }

  LivePublisher get _livePublisher {
    final publisher = widget.publisher ?? context.read<LivePublisher>();
    _bindPublisher(publisher);
    return publisher;
  }

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

  Future<void> _prepare(PublishSource source) async {
    if (_acting) return;
    final epoch = ++_actionEpoch;
    setState(() {
      _acting = true;
      _actionError = null;
    });
    try {
      // The selected device request starts before the first API call or await.
      await _livePublisher.begin(widget.broadcastId, source: source);
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

  Future<void> _edit(Broadcast b) async {
    final changed = await Navigator.of(context).push<bool>(
      MaterialPageRoute<bool>(builder: (_) => BroadcastFormScreen(existing: b)),
    );
    if (changed == true && mounted) await _load(silent: true, force: true);
  }

  Future<void> _delete(Broadcast b) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('\'${b.title}\' 삭제'),
        content: const Text('삭제한 방송은 목록에서 사라집니다.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('취소'),
          ),
          FilledButton(
            key: const Key('detail-delete-confirm'),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('삭제'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    try {
      await context.read<ApiClient>().deleteBroadcast(b.id, version: b.version);
      if (mounted) Navigator.of(context).pop();
    } on ApiException catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            e.status == 409 && e.code != 'version_conflict'
                ? '방송 중에는 삭제할 수 없습니다. 먼저 방송을 중지하세요.'
                : e.message,
          ),
        ),
      );
      await _load(silent: true, force: true);
    }
  }

  Widget _stage(BuildContext context, String state) {
    final scheme = Theme.of(context).colorScheme;
    final player = _player;
    final String message = switch (state) {
      'live' => _playbackStatus,
      'starting' => '방송을 준비 중입니다.',
      'stopping' => '방송을 종료하는 중입니다.',
      'ended' => '방송이 종료되었습니다.',
      _ => '아직 방송을 시작하지 않았습니다.',
    };
    return ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 960),
      child: AspectRatio(
        aspectRatio: 16 / 9,
        child: Container(
          color: Colors.black,
          child: player != null
              ? player.view
              : Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(
                        state == 'live' ? Icons.live_tv : Icons.tv_off_outlined,
                        size: 48,
                        color: scheme.onSurfaceVariant,
                      ),
                      const SizedBox(height: 8),
                      Text(message, textAlign: TextAlign.center),
                    ],
                  ),
                ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final b = _broadcast;
    final owner =
        b != null && context.watch<AuthController>().user?.id == b.ownerId;
    final state = _session?.state ?? b?.status ?? 'ready';
    final active = state == 'starting' || state == 'live' || state == 'stopping';
    final publisher = owner
        ? (widget.publisher ?? context.watch<LivePublisher>())
        : null;
    if (owner) _bindPublisher(publisher!);
    final session = _session;
    return Scaffold(
      appBar: AppBar(
        title: Text(b?.title ?? '방송'),
        actions: [
          if (owner && !active && !(publisher?.busy ?? false)) ...[
            IconButton(
              key: const Key('detail-edit'),
              tooltip: '방송 정보 수정',
              onPressed: () => _edit(b),
              icon: const Icon(Icons.edit_outlined),
            ),
            IconButton(
              key: const Key('detail-delete'),
              tooltip: '방송 삭제',
              onPressed: () => _delete(b),
              icon: const Icon(Icons.delete_outline),
            ),
          ],
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
                Center(child: _stage(context, state)),
                const SizedBox(height: 12),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    FilledButton.icon(
                      key: const Key('playback-play'),
                      onPressed: _player == null
                          ? null
                          : () => _play(withSound: false),
                      icon: const Icon(Icons.play_arrow),
                      label: const Text('재생'),
                    ),
                    OutlinedButton.icon(
                      key: const Key('playback-sound'),
                      onPressed: _player == null
                          ? null
                          : () => _play(withSound: true),
                      icon: const Icon(Icons.volume_up),
                      label: const Text('소리 켜기'),
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
                const SizedBox(height: 4),
                Text(_playbackStatus, key: const Key('playback-status')),
                const SizedBox(height: 16),
                Row(
                  children: [
                    BroadcastStatusBadge(status: state),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        b.title,
                        style: Theme.of(context).textTheme.headlineSmall,
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Text('방송자: ${b.ownerDisplayName}'),
                const SizedBox(height: 4),
                Text(
                  '상태: ${_statusLabel(state)}',
                  key: const Key('session-state'),
                ),
                const SizedBox(height: 4),
                Text('방송 등록: ${formatBroadcastDate(b.createdAt)}'),
                if (session != null)
                  Text(
                    session.endedAt == null
                        ? '방송 시작: ${formatBroadcastDate(session.startedAt)}'
                        : '최근 방송: ${formatBroadcastDate(session.startedAt)} ~ ${formatBroadcastDate(session.endedAt!)}',
                    key: const Key('session-times'),
                  ),
                if (b.description.isNotEmpty) ...[
                  const SizedBox(height: 16),
                  Text(b.description),
                ],
                const SizedBox(height: 20),
                if (owner) ...[
                  Text(
                    '방송 송출',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  const SizedBox(height: 8),
                ],
                if (state == 'starting')
                  const Text('회차를 준비 중입니다. 영상 준비 전에는 시청할 수 없습니다.'),
                if (state == 'stopping')
                  const Text('종료 처리 중입니다. 잠시 후 상태를 새로고침해 주세요.'),
                if (owner && (state == 'ready' || state == 'ended')) ...[
                  FilledButton.icon(
                    key: const Key('session-prepare'),
                    onPressed: _acting || publisher!.busy
                        ? null
                        : () => _prepare(PublishSource.screen),
                    icon: const Icon(Icons.screen_share_outlined),
                    label: const Text('화면 공유 시작'),
                  ),
                  const SizedBox(height: 8),
                  FilledButton.icon(
                    key: const Key('session-prepare-camera'),
                    onPressed: _acting || publisher!.busy
                        ? null
                        : () => _prepare(PublishSource.camera),
                    icon: const Icon(Icons.videocam_outlined),
                    label: const Text('카메라·마이크 시작'),
                  ),
                  const SizedBox(height: 4),
                  const Text(
                    'PC: 화면 공유 창에서 탭(또는 전체 화면)과 "소리 공유"를 선택하고 마이크를 허용하세요. '
                    '휴대폰: 카메라·마이크 권한을 허용하세요.',
                  ),
                ],
                if (owner &&
                    (publisher!.busy || state == 'starting' || state == 'live'))
                  OutlinedButton.icon(
                    key: const Key('session-cancel'),
                    onPressed: _canceling ? null : _cancel,
                    icon: const Icon(Icons.stop_circle_outlined),
                    label: const Text('방송 중지'),
                  ),
                if (owner && publisher!.broadcastId == widget.broadcastId) ...[
                  const SizedBox(height: 8),
                  Text(
                    '송출: ${_publishLabel(publisher.phase)}',
                    key: const Key('publish-phase'),
                  ),
                  if (publisher.message != null) Text(publisher.message!),
                  if (publisher.source == PublishSource.screen &&
                      !publisher.sharedAudio)
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
                  if (publisher.preview != null) ...[
                    const SizedBox(height: 8),
                    const Text('송출 미리보기'),
                    SizedBox(height: 240, child: publisher.preview),
                  ],
                ],
                if (_actionError != null)
                  Text(_actionError!, key: const Key('session-action-error')),
              ],
            ),
    );
  }

  String _publishLabel(PublishPhase phase) => switch (phase) {
    PublishPhase.idle => '대기',
    PublishPhase.capturing => '장치 권한 요청 중',
    PublishPhase.preparing => '회차 준비 중',
    PublishPhase.connecting => '서버 연결 중',
    PublishPhase.publishing => '송출 중',
    PublishPhase.stopping => '중지 중',
    PublishPhase.error => '오류',
  };

  String _statusLabel(String state) => switch (state) {
    'starting' => '준비 중',
    'stopping' => '종료 처리 중',
    'ended' => '종료',
    'live' => '방송 중',
    _ => '대기',
  };
}
