import 'dart:async';
import 'dart:math';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../api/session_start_key.dart';
import '../models/broadcast.dart';
import '../models/live_session.dart';
import '../state/auth_controller.dart';

/// Public detail and owner preparation. Media controls arrive in 04-B.
class BroadcastDetailScreen extends StatefulWidget {
  const BroadcastDetailScreen({super.key, required this.broadcastId});
  final String broadcastId;
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
  String? _error;
  String? _actionError;
  String? _pendingStartKey;
  Timer? _poll;

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
    super.dispose();
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
        if (session?.state == 'ended') _pendingStartKey = null;
      });
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

  String _newStartKey() {
    return createSessionStartKey(Random.secure());
  }

  Future<void> _prepare() async {
    if (_acting) return;
    final key = _pendingStartKey ??= _newStartKey();
    setState(() {
      _acting = true;
      _actionError = null;
    });
    try {
      final session = await context.read<ApiClient>().prepareSession(
        widget.broadcastId,
        source: 'screen',
        idempotencyKey: key,
      );
      if (!mounted) return;
      setState(() {
        _session = session;
        _pendingStartKey = null;
      });
      await _load(silent: true, force: true);
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _actionError = e.message;
      });
      if (e.status == 409) await _load(silent: true, force: true);
    } finally {
      if (mounted) {
        setState(() {
          _acting = false;
        });
      }
    }
  }

  Future<void> _cancel() async {
    if (_acting || _session == null) return;
    setState(() {
      _acting = true;
      _actionError = null;
    });
    try {
      final session = await context.read<ApiClient>().cancelSession(
        widget.broadcastId,
        _session!.id,
      );
      if (!mounted) return;
      setState(() {
        _session = session;
        _pendingStartKey = null;
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
          _acting = false;
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
                  const Text('회차를 준비 중입니다. 아직 송출이나 시청은 시작되지 않았습니다.'),
                if (state == 'stopping')
                  const Text('종료 처리 중입니다. 잠시 후 상태를 새로고침해 주세요.'),
                if (owner && (state == 'ready' || state == 'ended')) ...[
                  FilledButton(
                    key: const Key('session-prepare'),
                    onPressed: _acting ? null : _prepare,
                    child: const Text('방송 회차 준비'),
                  ),
                  const Text('화면 공유와 실제 송출은 이후 단계에서 제공됩니다.'),
                ],
                if (owner && state == 'starting')
                  OutlinedButton(
                    key: const Key('session-cancel'),
                    onPressed: _acting ? null : _cancel,
                    child: const Text('준비 취소'),
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
