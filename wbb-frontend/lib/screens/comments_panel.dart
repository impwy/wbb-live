import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../models/comment.dart';
import '../state/auth_controller.dart';
import 'home_screen.dart' show formatBroadcastDate;
import 'login_screen.dart';

/// Per-broadcast comments. Everyone reads; signed-in users write; only the
/// author sees edit/delete (the server enforces the same rule).
class CommentsPanel extends StatefulWidget {
  const CommentsPanel({super.key, required this.broadcastId});
  final String broadcastId;

  @override
  State<CommentsPanel> createState() => _CommentsPanelState();
}

class _CommentsPanelState extends State<CommentsPanel> {
  final _input = TextEditingController();
  List<Comment> _items = [];
  bool _loading = true;
  bool _sending = false;
  String? _error;
  Timer? _poll;
  int _epoch = 0;

  @override
  void initState() {
    super.initState();
    _load();
    _poll = Timer.periodic(const Duration(seconds: 5), (_) => _load());
  }

  @override
  void dispose() {
    _poll?.cancel();
    _epoch++;
    _input.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final epoch = ++_epoch;
    try {
      final items = await context.read<ApiClient>().listComments(
        widget.broadcastId,
      );
      if (!mounted || epoch != _epoch) return;
      setState(() {
        _items = items;
        _loading = false;
        _error = null;
      });
    } on ApiException catch (e) {
      if (!mounted || epoch != _epoch) return;
      setState(() {
        _loading = false;
        _error = e.message;
      });
    }
  }

  void _toast(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _send() async {
    final body = _input.text.trim();
    if (body.isEmpty || _sending) return;
    setState(() => _sending = true);
    try {
      await context.read<ApiClient>().createComment(widget.broadcastId, body);
      _input.clear();
      await _load();
    } on ApiException catch (e) {
      if (mounted) _toast('댓글 작성 실패: ${e.message}');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  Future<void> _edit(Comment c) async {
    final controller = TextEditingController(text: c.body);
    final body = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('댓글 수정'),
        content: TextField(
          key: const Key('comment-edit-field'),
          controller: controller,
          maxLength: 500,
          maxLines: 3,
          autofocus: true,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('취소'),
          ),
          FilledButton(
            key: const Key('comment-edit-save'),
            onPressed: () => Navigator.pop(context, controller.text.trim()),
            child: const Text('저장'),
          ),
        ],
      ),
    );
    controller.dispose();
    if (body == null || body.isEmpty || !mounted) return;
    try {
      await context.read<ApiClient>().updateComment(c.id, body);
      await _load();
    } on ApiException catch (e) {
      if (mounted) _toast('댓글 수정 실패: ${e.message}');
    }
  }

  Future<void> _delete(Comment c) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('댓글 삭제'),
        content: const Text('이 댓글을 삭제할까요?'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('취소'),
          ),
          FilledButton(
            key: const Key('comment-delete-confirm'),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('삭제'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    try {
      await context.read<ApiClient>().deleteComment(c.id);
      await _load();
    } on ApiException catch (e) {
      if (mounted) _toast('댓글 삭제 실패: ${e.message}');
    }
  }

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthController>();
    final me = auth.user?.id;
    final signedIn = auth.status == AuthStatus.authenticated;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          '댓글 ${_items.length}',
          style: Theme.of(context).textTheme.titleMedium,
        ),
        const SizedBox(height: 8),
        if (signedIn)
          Row(
            children: [
              Expanded(
                child: TextField(
                  key: const Key('comment-input'),
                  controller: _input,
                  maxLength: 500,
                  minLines: 1,
                  maxLines: 3,
                  decoration: const InputDecoration(
                    hintText: '댓글을 입력하세요',
                    counterText: '',
                  ),
                  onSubmitted: (_) => _send(),
                ),
              ),
              const SizedBox(width: 8),
              IconButton.filled(
                key: const Key('comment-send'),
                tooltip: '댓글 작성',
                onPressed: _sending ? null : _send,
                icon: const Icon(Icons.send),
              ),
            ],
          )
        else
          Row(
            children: [
              const Expanded(child: Text('로그인하면 댓글을 작성할 수 있습니다.')),
              TextButton(
                key: const Key('comment-login'),
                onPressed: () => Navigator.of(context).push(
                  MaterialPageRoute<void>(builder: (_) => const LoginScreen()),
                ),
                child: const Text('로그인'),
              ),
            ],
          ),
        const SizedBox(height: 8),
        if (_loading)
          const Padding(
            padding: EdgeInsets.all(12),
            child: Center(child: CircularProgressIndicator()),
          )
        else if (_error != null && _items.isEmpty)
          Text('댓글을 불러오지 못했습니다: $_error')
        else if (_items.isEmpty)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 12),
            child: Text('첫 댓글을 남겨 보세요.'),
          )
        else
          for (final c in _items.reversed)
            ListTile(
              key: Key('comment-${c.id}'),
              contentPadding: EdgeInsets.zero,
              dense: true,
              title: Text(c.body),
              subtitle: Text(
                '${c.authorName} · ${formatBroadcastDate(c.createdAt)}'
                '${c.edited ? ' · 수정됨' : ''}',
              ),
              trailing: c.authorId == me
                  ? PopupMenuButton<String>(
                      key: Key('comment-menu-${c.id}'),
                      onSelected: (v) => v == 'edit' ? _edit(c) : _delete(c),
                      itemBuilder: (_) => const [
                        PopupMenuItem(value: 'edit', child: Text('수정')),
                        PopupMenuItem(value: 'delete', child: Text('삭제')),
                      ],
                    )
                  : null,
            ),
      ],
    );
  }
}
