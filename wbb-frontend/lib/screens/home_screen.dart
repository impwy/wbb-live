import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../models/broadcast.dart';
import '../state/auth_controller.dart';
import 'broadcast_detail_screen.dart';
import 'broadcast_form_screen.dart';
import 'login_screen.dart';
import 'my_broadcasts_screen.dart';

enum HomeFilter { all, live }

/// Public broadcast list (works signed out). Live broadcasts can be filtered
/// and the first page refreshes periodically so new LIVE states appear.
class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key, this.openMyBroadcastsOnLoad = false});
  final bool openMyBroadcastsOnLoad;

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  List<Broadcast> _items = [];
  String _nextCursor = '';
  bool _loading = true;
  bool _pagedFurther = false;
  String? _error;
  HomeFilter _filter = HomeFilter.all;
  int _loadEpoch = 0;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    _load();
    _poll = Timer.periodic(const Duration(seconds: 10), (_) {
      // Keep extra pages the viewer loaded; only refresh the first page.
      if (!_pagedFurther && !_loading) _load(silent: true);
    });
    if (widget.openMyBroadcastsOnLoad) {
      WidgetsBinding.instance.addPostFrameCallback((_) async {
        if (!mounted) return;
        await Navigator.of(context).push(
          MaterialPageRoute<void>(builder: (_) => const MyBroadcastsScreen()),
        );
        if (mounted) _load();
      });
    }
  }

  @override
  void dispose() {
    _poll?.cancel();
    _loadEpoch++;
    super.dispose();
  }

  Future<void> _load({bool more = false, bool silent = false}) async {
    final epoch = ++_loadEpoch;
    if (!silent) {
      setState(() {
        _loading = true;
        _error = null;
      });
    }
    try {
      final page = await context.read<ApiClient>().listBroadcasts(
        cursor: more && _nextCursor.isNotEmpty ? _nextCursor : null,
        liveOnly: _filter == HomeFilter.live,
      );
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _items = more ? [..._items, ...page.items] : page.items;
        _nextCursor = page.nextCursor;
        _pagedFurther = more;
        _loading = false;
        _error = null;
      });
    } on ApiException catch (e) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        if (!silent) _error = e.message;
        _loading = false;
      });
    }
  }

  void _setFilter(HomeFilter filter) {
    if (filter == _filter) return;
    setState(() {
      _filter = filter;
      _items = [];
      _nextCursor = '';
      _pagedFurther = false;
    });
    _load();
  }

  Future<void> _open(Broadcast b) async {
    await Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => BroadcastDetailScreen(broadcastId: b.id),
      ),
    );
    if (mounted) _load(silent: true);
  }

  Future<void> _create() async {
    final auth = context.read<AuthController>();
    if (auth.status != AuthStatus.authenticated) {
      await Navigator.of(context).push(
        MaterialPageRoute<void>(builder: (_) => const LoginScreen()),
      );
      if (!mounted ||
          context.read<AuthController>().status != AuthStatus.authenticated) {
        return;
      }
    }
    final created = await Navigator.of(context).push<bool>(
      MaterialPageRoute<bool>(builder: (_) => const BroadcastFormScreen()),
    );
    if (created == true && mounted) _load();
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
        title: const Text('WBB Live'),
        actions: [
          if (auth.status == AuthStatus.authenticated) ...[
            IconButton(
              key: const Key('home-my-broadcasts'),
              tooltip: '내 방송',
              icon: const Icon(Icons.videocam_outlined),
              onPressed: () async {
                await Navigator.of(context).push(
                  MaterialPageRoute<void>(
                    builder: (_) => const MyBroadcastsScreen(),
                  ),
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
              key: const Key('home-logout'),
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
              key: const Key('home-login'),
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute<void>(builder: (_) => const LoginScreen()),
              ),
              child: const Text('로그인'),
            ),
        ],
      ),
      floatingActionButton: auth.status == AuthStatus.unknown
          ? null
          : FloatingActionButton.extended(
              key: const Key('home-create'),
              onPressed: _create,
              icon: const Icon(Icons.add),
              label: const Text('방송 등록'),
            ),
      body: RefreshIndicator(
        onRefresh: () => _load(),
        child: CustomScrollView(
          physics: const AlwaysScrollableScrollPhysics(),
          slivers: [
            SliverToBoxAdapter(
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
                child: Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    ChoiceChip(
                      key: const Key('home-filter-all'),
                      label: const Text('전체 방송'),
                      selected: _filter == HomeFilter.all,
                      onSelected: (_) => _setFilter(HomeFilter.all),
                    ),
                    ChoiceChip(
                      key: const Key('home-filter-live'),
                      avatar: const Icon(Icons.circle, size: 10, color: Colors.red),
                      label: const Text('방송 중'),
                      selected: _filter == HomeFilter.live,
                      onSelected: (_) => _setFilter(HomeFilter.live),
                    ),
                  ],
                ),
              ),
            ),
            ..._body(context),
            const SliverToBoxAdapter(child: SizedBox(height: 88)),
          ],
        ),
      ),
    );
  }

  List<Widget> _body(BuildContext context) {
    if (_loading && _items.isEmpty) {
      return const [
        SliverFillRemaining(
          hasScrollBody: false,
          child: Center(child: CircularProgressIndicator()),
        ),
      ];
    }
    if (_error != null && _items.isEmpty) {
      return [
        SliverToBoxAdapter(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Column(
              children: [
                Text('불러오기 실패: $_error'),
                const SizedBox(height: 12),
                OutlinedButton(
                  key: const Key('home-retry'),
                  onPressed: () => _load(),
                  child: const Text('다시 시도'),
                ),
              ],
            ),
          ),
        ),
      ];
    }
    if (_items.isEmpty) {
      return [
        SliverToBoxAdapter(
          child: Padding(
            padding: const EdgeInsets.all(32),
            child: Center(
              child: Text(
                _filter == HomeFilter.live
                    ? '지금 방송 중인 채널이 없습니다.'
                    : '아직 방송이 없습니다.',
              ),
            ),
          ),
        ),
      ];
    }
    return [
      SliverPadding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        sliver: SliverGrid(
          gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
            maxCrossAxisExtent: 380,
            mainAxisSpacing: 16,
            crossAxisSpacing: 16,
            mainAxisExtent: 300,
          ),
          delegate: SliverChildBuilderDelegate(
            (context, index) => BroadcastCard(
              broadcast: _items[index],
              onTap: () => _open(_items[index]),
            ),
            childCount: _items.length,
          ),
        ),
      ),
      if (_nextCursor.isNotEmpty)
        SliverToBoxAdapter(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Center(
              child: OutlinedButton(
                key: const Key('home-more'),
                onPressed: _loading ? null : () => _load(more: true),
                child: _loading
                    ? const SizedBox(
                        height: 18,
                        width: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('더 보기'),
              ),
            ),
          ),
        ),
    ];
  }
}

/// One broadcast in the public list.
class BroadcastCard extends StatelessWidget {
  const BroadcastCard({super.key, required this.broadcast, required this.onTap});

  final Broadcast broadcast;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final b = broadcast;
    final scheme = Theme.of(context).colorScheme;
    final live = b.status == 'live';
    return Card(
      key: Key('broadcast-card-${b.id}'),
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: onTap,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            AspectRatio(
              aspectRatio: 16 / 9,
              child: Container(
                color: live
                    ? scheme.primaryContainer
                    : scheme.surfaceContainerHighest,
                child: Stack(
                  children: [
                    Center(
                      child: Icon(
                        live ? Icons.live_tv : Icons.tv_off_outlined,
                        size: 48,
                        color: live
                            ? scheme.onPrimaryContainer
                            : scheme.onSurfaceVariant,
                      ),
                    ),
                    Positioned(
                      left: 8,
                      top: 8,
                      child: BroadcastStatusBadge(status: b.status),
                    ),
                  ],
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 10, 12, 0),
              child: Text(
                b.title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.titleMedium,
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 2, 12, 0),
              child: Text(
                '${b.ownerDisplayName} · ${formatBroadcastDate(b.createdAt)}',
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
            if (b.description.isNotEmpty)
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 4, 12, 0),
                child: Text(
                  b.description,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ),
          ],
        ),
      ),
    );
  }
}

/// LIVE/ready/ended label shared by list and detail screens.
class BroadcastStatusBadge extends StatelessWidget {
  const BroadcastStatusBadge({super.key, required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (status) {
      'live' => ('LIVE', Colors.red),
      'starting' => ('준비 중', Colors.orange),
      'stopping' => ('종료 중', Colors.orange),
      'ended' => ('종료', Colors.grey),
      _ => ('대기', Colors.blueGrey),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: status == 'live' ? color : color.withValues(alpha: 0.85),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        label,
        style: const TextStyle(
          color: Colors.white,
          fontSize: 12,
          fontWeight: FontWeight.bold,
        ),
      ),
    );
  }
}

String formatBroadcastDate(DateTime d) {
  final l = d.toLocal();
  String two(int v) => v.toString().padLeft(2, '0');
  return '${l.year}.${two(l.month)}.${two(l.day)} ${two(l.hour)}:${two(l.minute)}';
}
