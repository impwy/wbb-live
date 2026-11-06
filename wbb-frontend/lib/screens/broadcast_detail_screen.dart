import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../models/broadcast.dart';

/// Public broadcast detail. Live playback (LL-HLS) arrives with the media
/// tickets; this screen shows metadata and state for now.
class BroadcastDetailScreen extends StatefulWidget {
  const BroadcastDetailScreen({super.key, required this.broadcastId});

  final String broadcastId;

  @override
  State<BroadcastDetailScreen> createState() => _BroadcastDetailScreenState();
}

class _BroadcastDetailScreenState extends State<BroadcastDetailScreen> {
  Broadcast? _broadcast;
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final b = await context.read<ApiClient>().getBroadcast(widget.broadcastId);
      if (!mounted) return;
      setState(() {
        _broadcast = b;
        _loading = false;
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.status == 404 ? '방송을 찾을 수 없습니다.' : e.message;
        _loading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final b = _broadcast;
    return Scaffold(
      appBar: AppBar(title: Text(b?.title ?? '방송')),
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
                  : ListView(padding: const EdgeInsets.all(16), children: [
                      Text(b.title, style: Theme.of(context).textTheme.headlineSmall),
                      const SizedBox(height: 8),
                      Text('방송자: ${b.ownerDisplayName}'),
                      const SizedBox(height: 4),
                      Text('상태: ${b.statusLabel}'),
                      const SizedBox(height: 4),
                      Text('시작 대기 생성: ${b.createdAt.toLocal()}'),
                      if (b.description.isNotEmpty) ...[
                        const SizedBox(height: 16),
                        Text(b.description),
                      ],
                    ]),
    );
  }
}

extension on Broadcast {
  String get statusLabel => switch (status) {
        'live' => '방송 중',
        'ended' => '종료',
        _ => '대기',
      };
}
