import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../media/file_video.dart';
import '../models/recording.dart';
import 'broadcast_detail_screen.dart';
import 'home_screen.dart' show formatBroadcastDate;

/// Replay detail: the stored recording with its broadcast information.
class RecordingScreen extends StatefulWidget {
  const RecordingScreen({super.key, required this.recordingId});
  final String recordingId;

  @override
  State<RecordingScreen> createState() => _RecordingScreenState();
}

class _RecordingScreenState extends State<RecordingScreen> {
  Recording? _recording;
  Widget? _player;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final api = context.read<ApiClient>();
    try {
      final r = await api.getRecording(widget.recordingId);
      if (!mounted) return;
      setState(() {
        _recording = r;
        _player = fileVideoView(api.recordingMediaUrl(r.id));
      });
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    }
  }

  @override
  Widget build(BuildContext context) {
    final r = _recording;
    return Scaffold(
      appBar: AppBar(title: Text(r?.broadcastTitle ?? '다시보기')),
      body: _error != null
          ? Center(child: Text('다시보기를 불러오지 못했습니다: $_error'))
          : r == null
          ? const Center(child: CircularProgressIndicator())
          : ListView(
              padding: const EdgeInsets.all(16),
              children: [
                Center(
                  child: ConstrainedBox(
                    constraints: BoxConstraints(
                      maxWidth:
                          MediaQuery.sizeOf(context).height * 0.5 * 16 / 9,
                    ),
                    child: AspectRatio(
                      aspectRatio: 16 / 9,
                      child: ColoredBox(color: Colors.black, child: _player),
                    ),
                  ),
                ),
                const SizedBox(height: 12),
                Text(
                  r.broadcastTitle,
                  style: Theme.of(context).textTheme.headlineSmall,
                ),
                const SizedBox(height: 4),
                Text('방송자: ${r.ownerName}'),
                Text('방송 일시: ${formatBroadcastDate(r.startedAt)}'),
                Text(
                  '길이: ${r.durationLabel} · '
                  '${(r.sizeBytes / (1024 * 1024)).toStringAsFixed(1)}MB',
                ),
                const SizedBox(height: 12),
                Align(
                  alignment: Alignment.centerLeft,
                  child: OutlinedButton.icon(
                    key: const Key('recording-open-broadcast'),
                    onPressed: () => Navigator.of(context).push(
                      MaterialPageRoute<void>(
                        builder: (_) =>
                            BroadcastDetailScreen(broadcastId: r.broadcastId),
                      ),
                    ),
                    icon: const Icon(Icons.live_tv),
                    label: const Text('방송 페이지로'),
                  ),
                ),
              ],
            ),
    );
  }
}

/// Replays of one broadcast, shown on its detail page.
class BroadcastRecordings extends StatefulWidget {
  const BroadcastRecordings({super.key, required this.broadcastId});
  final String broadcastId;

  @override
  State<BroadcastRecordings> createState() => _BroadcastRecordingsState();
}

class _BroadcastRecordingsState extends State<BroadcastRecordings> {
  List<Recording> _items = [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final items = await context.read<ApiClient>().listRecordings(
        broadcastId: widget.broadcastId,
      );
      if (mounted) setState(() => _items = items);
    } on ApiException {
      // Replays are optional; hide on errors.
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_items.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          '다시보기 ${_items.length}',
          style: Theme.of(context).textTheme.titleMedium,
        ),
        for (final r in _items)
          ListTile(
            key: Key('broadcast-recording-${r.id}'),
            dense: true,
            contentPadding: EdgeInsets.zero,
            leading: const Icon(Icons.play_circle_outline),
            title: Text(formatBroadcastDate(r.startedAt)),
            subtitle: Text('길이 ${r.durationLabel}'),
            onTap: () => Navigator.of(context).push(
              MaterialPageRoute<void>(
                builder: (_) => RecordingScreen(recordingId: r.id),
              ),
            ),
          ),
        const SizedBox(height: 8),
      ],
    );
  }
}
