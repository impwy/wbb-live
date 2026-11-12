import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../models/broadcast.dart';
import '../models/recording.dart';
import '../state/auth_controller.dart';
import 'broadcast_detail_screen.dart';
import 'broadcast_form_screen.dart';
import 'login_screen.dart';
import 'my_broadcasts_screen.dart';
import 'recording_screen.dart';
import 'signup_screen.dart';
import '../ui/visuals.dart';

enum HomeFilter { all, live, replay }

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
  List<Recording> _recordings = [];
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
      if (_filter == HomeFilter.replay) {
        final items = await context.read<ApiClient>().listRecordings();
        if (!mounted || epoch != _loadEpoch) return;
        setState(() {
          _recordings = items;
          _items = [];
          _nextCursor = '';
          _loading = false;
          _error = null;
        });
        return;
      }
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
      await Navigator.of(context)
          .push(MaterialPageRoute<void>(builder: (_) => const LoginScreen()));
      if (!mounted ||
          context.read<AuthController>().status != AuthStatus.authenticated) {
        return;
      }
    }
    final created = await Navigator.of(context).push<bool>(
      MaterialPageRoute<bool>(
        builder: (_) => const BroadcastFormScreen(openDetailAfterCreate: true),
      ),
    );
    if (created == true && mounted) _load();
  }

  Future<void> _openRecording(Recording r) async {
    await Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => RecordingScreen(recordingId: r.id),
      ),
    );
  }

  Future<void> _logout() async {
    try {
      await context.read<AuthController>().logout();
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('로그아웃 실패: ${e.message}')));
      }
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
    final signedIn = auth.status == AuthStatus.authenticated;
    return Scaffold(
      appBar: AppBar(
        titleSpacing: 16,
        title: const Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.live_tv_rounded, color: WbbColors.accent),
            SizedBox(width: 8),
            Text('WBB Live'),
          ],
        ),
        actions: [
          if (auth.status != AuthStatus.unknown)
            Padding(
              padding: const EdgeInsets.only(right: 8),
              child: FilledButton.tonalIcon(
                key: const Key('home-create'),
                onPressed: _create,
                icon: const Icon(Icons.videocam, size: 18),
                label: const Text('방송하기'),
              ),
            ),
          if (signedIn) ...[
            IconButton(
              key: const Key('home-my-broadcasts'),
              tooltip: '내 방송',
              icon: const Icon(Icons.video_library_outlined),
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
              padding: const EdgeInsets.symmetric(horizontal: 4),
              child: Row(
                children: [
                  ChannelAvatar(
                    seed: auth.user?.id ?? '',
                    name: auth.user?.displayName ?? '',
                    size: 28,
                  ),
                  const SizedBox(width: 6),
                  Text(auth.user?.displayName ?? ''),
                ],
              ),
            ),
            IconButton(
              key: const Key('home-logout'),
              tooltip: '로그아웃',
              icon: const Icon(Icons.logout),
              onPressed: _logout,
            ),
            const SizedBox(width: 4),
          ] else if (auth.status == AuthStatus.unauthenticated) ...[
            TextButton(
              key: const Key('home-signup'),
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute<void>(builder: (_) => const SignupScreen()),
              ),
              child: const Text('회원가입'),
            ),
            Padding(
              padding: const EdgeInsets.only(right: 12, left: 4),
              child: FilledButton(
                key: const Key('home-login'),
                onPressed: () => Navigator.of(context).push(
                  MaterialPageRoute<void>(builder: (_) => const LoginScreen()),
                ),
                child: const Text('로그인'),
              ),
            ),
          ],
        ],
      ),
      body: LayoutBuilder(
        builder: (context, constraints) {
          final wide = constraints.maxWidth >= 1000;
          final content = RefreshIndicator(
            onRefresh: () => _load(),
            child: CustomScrollView(
              physics: const AlwaysScrollableScrollPhysics(),
              slivers: [
                SliverToBoxAdapter(child: _tabs()),
                ..._body(context),
                const SliverToBoxAdapter(child: SizedBox(height: 32)),
              ],
            ),
          );
          if (!wide) return content;
          return Row(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              SizedBox(width: 240, child: _sidebar()),
              const VerticalDivider(width: 1),
              Expanded(child: content),
            ],
          );
        },
      ),
    );
  }

  Widget _tabs() {
    Widget tab(HomeFilter f, Key key, String label, IconData icon) {
      final selected = _filter == f;
      return ChoiceChip(
        key: key,
        avatar: Icon(
          icon,
          size: 16,
          color: f == HomeFilter.live ? WbbColors.live : null,
        ),
        label: Text(label),
        selected: selected,
        showCheckmark: false,
        onSelected: (_) => _setFilter(f),
      );
    }

    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 20, 20, 8),
      child: Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          tab(
            HomeFilter.all,
            const Key('home-filter-all'),
            '전체 방송',
            Icons.grid_view_rounded,
          ),
          tab(
            HomeFilter.live,
            const Key('home-filter-live'),
            '방송 중',
            Icons.circle,
          ),
          tab(
            HomeFilter.replay,
            const Key('home-filter-replay'),
            '다시보기',
            Icons.video_library_outlined,
          ),
        ],
      ),
    );
  }

  /// Desktop sidebar: channels that are live in the loaded list.
  Widget _sidebar() {
    final live = _items.where((b) => b.status == 'live').toList();
    return Container(
      color: WbbColors.surface,
      child: ListView(
        padding: const EdgeInsets.symmetric(vertical: 12),
        children: [
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 4, 16, 8),
            child: Text(
              '지금 방송 중',
              style: TextStyle(fontWeight: FontWeight.w700, fontSize: 13),
            ),
          ),
          if (live.isEmpty)
            const Padding(
              padding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
              child: Text(
                '방송 중인 채널이 없습니다.',
                style: TextStyle(color: WbbColors.muted, fontSize: 13),
              ),
            ),
          for (final b in live)
            InkWell(
              key: Key('sidebar-${b.id}'),
              onTap: () => _open(b),
              child: Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: 12,
                  vertical: 6,
                ),
                child: Row(
                  children: [
                    ChannelAvatar(
                      seed: b.ownerId,
                      name: b.ownerDisplayName,
                      size: 30,
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            b.ownerDisplayName,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: const TextStyle(
                              fontWeight: FontWeight.w600,
                              fontSize: 13,
                            ),
                          ),
                          Text(
                            b.title,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: const TextStyle(
                              color: WbbColors.muted,
                              fontSize: 12,
                            ),
                          ),
                        ],
                      ),
                    ),
                    const Icon(Icons.circle, size: 8, color: WbbColors.live),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }

  Widget _sectionTitle(String text) => SliverToBoxAdapter(
    child: Padding(
      padding: const EdgeInsets.fromLTRB(20, 16, 20, 10),
      child: Text(
        text,
        style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
      ),
    ),
  );

  Widget _grid(List<Widget> cards) => SliverPadding(
    padding: const EdgeInsets.symmetric(horizontal: 20),
    sliver: SliverGrid(
      gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
        maxCrossAxisExtent: 360,
        mainAxisSpacing: 20,
        crossAxisSpacing: 16,
        childAspectRatio: 1.18,
      ),
      delegate: SliverChildListDelegate(cards),
    ),
  );

  Widget _message(String text) => SliverToBoxAdapter(
    child: Padding(
      padding: const EdgeInsets.all(32),
      child: Center(
        child: Text(text, style: const TextStyle(color: WbbColors.muted)),
      ),
    ),
  );

  List<Widget> _body(BuildContext context) {
    if (_filter == HomeFilter.replay && !_loading && _error == null) {
      if (_recordings.isEmpty) return [_message('아직 다시보기 영상이 없습니다.')];
      return [
        _sectionTitle('다시보기'),
        _grid([
          for (final r in _recordings)
            RecordingCard(
              key: Key('recording-card-${r.id}'),
              recording: r,
              onTap: () => _openRecording(r),
            ),
        ]),
      ];
    }
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
        _message(
          _filter == HomeFilter.live ? '지금 방송 중인 채널이 없습니다.' : '아직 방송이 없습니다.',
        ),
      ];
    }
    Widget card(Broadcast b) =>
        BroadcastCard(broadcast: b, onTap: () => _open(b));
    final slivers = <Widget>[];
    if (_filter == HomeFilter.live) {
      slivers
        ..add(_sectionTitle('지금 방송 중'))
        ..add(_grid([for (final b in _items) card(b)]));
    } else {
      final live = _items.where((b) => b.status == 'live').toList();
      final rest = _items.where((b) => b.status != 'live').toList();
      if (live.isNotEmpty) {
        slivers
          ..add(_sectionTitle('지금 방송 중'))
          ..add(_grid([for (final b in live) card(b)]));
      }
      if (rest.isNotEmpty) {
        slivers
          ..add(_sectionTitle(live.isEmpty ? '방송 목록' : '다른 방송'))
          ..add(_grid([for (final b in rest) card(b)]));
      }
    }
    if (_nextCursor.isNotEmpty) {
      slivers.add(
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
      );
    }
    return slivers;
  }
}

/// One broadcast in the public list: thumbnail, then avatar + title + channel.
class BroadcastCard extends StatelessWidget {
  const BroadcastCard({
    super.key,
    required this.broadcast,
    required this.onTap,
  });

  final Broadcast broadcast;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final b = broadcast;
    final live = b.status == 'live';
    return InkWell(
      key: Key('broadcast-card-${b.id}'),
      onTap: onTap,
      borderRadius: BorderRadius.circular(8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          GeneratedThumbnail(
            seed: b.id,
            title: b.title,
            dimmed: !live,
            topLeft: BroadcastStatusBadge(status: b.status),
          ),
          const SizedBox(height: 10),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              ChannelAvatar(
                seed: b.ownerId,
                name: b.ownerDisplayName,
                size: 36,
                live: live,
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      b.title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        fontWeight: FontWeight.w700,
                        fontSize: 15,
                      ),
                    ),
                    const SizedBox(height: 2),
                    Text(
                      '${b.ownerDisplayName} · ${formatBroadcastDate(b.createdAt)}',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: WbbColors.muted,
                        fontSize: 13,
                      ),
                    ),
                    if (b.description.isNotEmpty)
                      Text(
                        b.description,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                          color: WbbColors.muted,
                          fontSize: 12,
                        ),
                      ),
                  ],
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

/// One replay in the list.
class RecordingCard extends StatelessWidget {
  const RecordingCard({
    super.key,
    required this.recording,
    required this.onTap,
  });
  final Recording recording;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final r = recording;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          GeneratedThumbnail(
            seed: r.broadcastId,
            title: r.broadcastTitle,
            topLeft: const OverlayLabel('다시보기'),
            bottomLeft: OverlayLabel(r.durationLabel),
          ),
          const SizedBox(height: 10),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              ChannelAvatar(seed: r.broadcastId, name: r.ownerName, size: 36),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      r.broadcastTitle,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        fontWeight: FontWeight.w700,
                        fontSize: 15,
                      ),
                    ),
                    Text(
                      '${r.ownerName} · ${formatBroadcastDate(r.startedAt)}',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: WbbColors.muted,
                        fontSize: 13,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ],
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
      'live' => ('LIVE', WbbColors.live),
      'starting' => ('준비 중', Colors.orange.shade700),
      'stopping' => ('종료 중', Colors.orange.shade700),
      'ended' => ('종료', Colors.black54),
      _ => ('대기', Colors.black54),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color,
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: const TextStyle(
          color: Colors.white,
          fontSize: 12,
          fontWeight: FontWeight.w700,
          letterSpacing: 0.3,
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
