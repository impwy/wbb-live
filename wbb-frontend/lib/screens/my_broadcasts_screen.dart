import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../models/broadcast.dart';
import 'broadcast_form_screen.dart';
import 'broadcast_detail_screen.dart';

/// The signed-in broadcaster's own broadcasts: create, edit, delete.
/// The API has no owner filter yet, so pages are walked and filtered by
/// ownerId client-side (noted as a contract gap for Codex).
class MyBroadcastsScreen extends StatefulWidget {
  const MyBroadcastsScreen({super.key});

  @override
  State<MyBroadcastsScreen> createState() => _MyBroadcastsScreenState();
}

class _MyBroadcastsScreenState extends State<MyBroadcastsScreen> {
  List<Broadcast> _mine = [];
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
      final api = context.read<ApiClient>();
      final myUserId = (await api.me()).id;
      final mine = <Broadcast>[];
      var cursor = '';
      while (true) {
        final page = await api.listBroadcasts(cursor: cursor.isEmpty ? null : cursor);
        mine.addAll(page.items.where((b) => b.ownerId == myUserId));
        if (page.nextCursor.isEmpty) break;
        cursor = page.nextCursor;
      }
      if (!mounted) return;
      setState(() {
        _mine = mine;
        _loading = false;
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.message;
        _loading = false;
      });
    }
  }

  Future<void> _delete(Broadcast b) async {
    final api = context.read<ApiClient>();
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('\'${b.title}\' 삭제'),
        content: const Text('삭제한 방송은 목록에서 사라집니다.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('취소')),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('삭제')),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await api.deleteBroadcast(b.id, version: b.version);
      await _load();
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('내 방송')),
      floatingActionButton: FloatingActionButton(
        key: const Key('my-broadcasts-create'),
        tooltip: '방송 등록',
        onPressed: () async {
          final created = await Navigator.of(context).push<bool>(
            MaterialPageRoute<bool>(builder: (_) => const BroadcastFormScreen()),
          );
          if (created == true) _load();
        },
        child: const Icon(Icons.add),
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text('불러오기 실패: $_error'),
                      const SizedBox(height: 12),
                      OutlinedButton(
                        key: const Key('my-broadcasts-retry'),
                        onPressed: _load,
                        child: const Text('다시 시도'),
                      ),
                    ],
                  ),
                )
              : _mine.isEmpty
                  ? const Center(child: Text('등록한 방송이 없습니다. + 버튼으로 만드세요.'))
                  : ListView.builder(
                      itemCount: _mine.length,
                      itemBuilder: (context, index) {
                        final b = _mine[index];
                        return ListTile(
                          title: Text(b.title),
                          onTap: () async {
                            await Navigator.of(context).push<void>(
                              MaterialPageRoute<void>(builder: (_) => BroadcastDetailScreen(broadcastId: b.id)),
                            );
                            if (mounted) _load();
                          },
                          subtitle: Text(
                            '${_statusLabel(b.status)} · 수정 ${_formatDate(b.updatedAt)}',
                          ),
                          trailing: PopupMenuButton<String>(
                            onSelected: (action) async {
                              if (action == 'edit') {
                                final changed = await Navigator.of(context).push<bool>(
                                  MaterialPageRoute<bool>(
                                    builder: (_) => BroadcastFormScreen(existing: b),
                                  ),
                                );
                                if (changed == true) _load();
                              } else if (action == 'delete') {
                                _delete(b);
                              }
                            },
                            itemBuilder: (context) => const [
                              PopupMenuItem(value: 'edit', child: Text('수정')),
                              PopupMenuItem(value: 'delete', child: Text('삭제')),
                            ],
                          ),
                        );
                      },
                    ),
    );
  }

  String _formatDate(DateTime d) =>
      '${d.year}.${d.month.toString().padLeft(2, '0')}.${d.day.toString().padLeft(2, '0')}';

  String _statusLabel(String status) => switch (status) {
        'live' => '방송 중',
        'starting' => '준비 중',
        'stopping' => '종료 처리 중',
        'ended' => '종료',
        _ => '대기',
      };
}
