import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../models/broadcast.dart';
import 'broadcast_detail_screen.dart';
import 'login_screen.dart';
import 'my_broadcasts_screen.dart';
import '../state/auth_controller.dart';


/// Public broadcast list (works signed out).
class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  List<Broadcast> _items = [];
  String _nextCursor = '';
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load({bool more = false}) async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final page = await context.read<ApiClient>().listBroadcasts(
            cursor: more ? (_nextCursor.isEmpty ? null : _nextCursor) : null,
          );
      if (!mounted) return;
      setState(() {
        _items = more ? [..._items, ...page.items] : page.items;
        _nextCursor = page.nextCursor;
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

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthController>();
    if (auth.sessionExpired) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('로그인이 만료되었습니다. 다시 로그인해 주세요.')),
        );
        context.read<AuthController>().clearSessionExpired();
      });
    }
    return Scaffold(
      appBar: AppBar(
        title: const Text('WBB'),
        actions: [
          if (auth.status == AuthStatus.authenticated) ...[
            IconButton(
              tooltip: '내 방송',
              icon: const Icon(Icons.videocam_outlined),
              onPressed: () async {
                await Navigator.of(context).push(
                  MaterialPageRoute<void>(builder: (_) => const MyBroadcastsScreen()),
                );
                // Broadcasts may have been created/edited/deleted while away.
                if (mounted) _load();
              },
            ),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              child: Center(child: Text(auth.user?.displayName ?? '')),
            ),
            IconButton(
              tooltip: '로그아웃',
              icon: const Icon(Icons.logout),
              onPressed: () async {
                try {
                  await context.read<AuthController>().logout();
                } on ApiException catch (e) {
                  if (context.mounted) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(content: Text('로그아웃 실패: ${e.message}')),
                    );
                  }
                }
              },
            ),
          ] else if (auth.status == AuthStatus.unauthenticated)
            TextButton(
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute<void>(builder: (_) => const LoginScreen()),
              ),
              child: const Text('로그인'),
            ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () => _load(),
        child: _loading && _items.isEmpty
            ? const Center(child: CircularProgressIndicator())
            : _error != null && _items.isEmpty
                ? ListView(children: [
                    Padding(
                      padding: const EdgeInsets.all(24),
                      child: Column(children: [
                        Text('불러오기 실패: $_error'),
                        const SizedBox(height: 12),
                        OutlinedButton(
                          key: const Key('home-retry'),
                          onPressed: () => _load(),
                          child: const Text('다시 시도'),
                        ),
                      ]),
                    ),
                  ])
                : _items.isEmpty
                    ? ListView(children: const [
                        Padding(
                          padding: EdgeInsets.all(24),
                          child: Center(child: Text('아직 방송이 없습니다.')),
                        ),
                      ])
                    : ListView.builder(
                        itemCount: _items.length + (_nextCursor.isNotEmpty ? 1 : 0),
                        itemBuilder: (context, index) {
                          if (index >= _items.length) {
                            return Padding(
                              padding: const EdgeInsets.all(12),
                              child: OutlinedButton(
                                onPressed: _loading ? null : () => _load(more: true),
                                child: _loading
                                    ? const SizedBox(
                                        height: 18, width: 18,
                                        child: CircularProgressIndicator(strokeWidth: 2))
                                    : const Text('더 보기'),
                              ),
                            );
                          }
                          final b = _items[index];
                          return ListTile(
                            leading: _StatusBadge(status: b.status),
                            title: Text(b.title),
                            subtitle: Text('${b.ownerDisplayName} · ${_formatDate(b.createdAt)}'),
                            onTap: () => Navigator.of(context).push(
                              MaterialPageRoute<void>(
                                builder: (_) => BroadcastDetailScreen(broadcastId: b.id),
                              ),
                            ),
                          );
                        },
                      ),
      ),
    );
  }

  String _formatDate(DateTime d) => '${d.year}.${d.month.toString().padLeft(2, '0')}.${d.day.toString().padLeft(2, '0')}';
}

class _StatusBadge extends StatelessWidget {
  const _StatusBadge({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (status) {
      'live' => ('LIVE', Colors.red),
      'ended' => ('종료', Colors.grey),
      _ => ('대기', Colors.blueGrey),
    };
    return Container(
      width: 48,
      height: 24,
      alignment: Alignment.center,
      decoration: BoxDecoration(color: color.withValues(alpha: 0.2), borderRadius: BorderRadius.circular(6)),
      child: Text(label, style: TextStyle(color: color, fontSize: 11, fontWeight: FontWeight.bold)),
    );
  }
}
