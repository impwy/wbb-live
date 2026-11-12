import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../media/event_stream.dart';
import '../models/chat_message.dart';
import '../state/auth_controller.dart';
import 'login_screen.dart';

/// Live chat for a broadcast: everyone reads in real time over SSE; signed-in
/// users send messages.
class ChatPanel extends StatefulWidget {
  const ChatPanel({super.key, required this.broadcastId, this.height = 320});
  final String broadcastId;
  final double height;

  @override
  State<ChatPanel> createState() => _ChatPanelState();
}

class _ChatPanelState extends State<ChatPanel> {
  final _input = TextEditingController();
  final _scroll = ScrollController();
  final List<ChatMessage> _messages = [];
  final Set<String> _seen = {};
  EventStream? _stream;
  bool _connected = false;
  bool _sending = false;

  @override
  void initState() {
    super.initState();
    _stream = openEventStream(
      context.read<ApiClient>().chatStreamUrl(widget.broadcastId),
      onData: _onData,
      onState: (connected) {
        if (mounted && connected != _connected) {
          setState(() => _connected = connected);
        }
      },
    );
  }

  @override
  void dispose() {
    _stream?.close();
    _input.dispose();
    _scroll.dispose();
    super.dispose();
  }

  void _onData(String data) {
    final ChatMessage message;
    try {
      message = ChatMessage.fromJson(jsonDecode(data) as Map<String, dynamic>);
    } catch (_) {
      return;
    }
    _add(message);
  }

  void _add(ChatMessage message) {
    if (!mounted || !_seen.add(message.id)) return;
    setState(() {
      _messages.add(message);
      if (_messages.length > 200) {
        _seen.remove(_messages.removeAt(0).id);
      }
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scroll.hasClients) {
        _scroll.jumpTo(_scroll.position.maxScrollExtent);
      }
    });
  }

  Future<void> _send() async {
    final text = _input.text.trim();
    if (text.isEmpty || _sending) return;
    setState(() => _sending = true);
    try {
      final sent = await context.read<ApiClient>().sendChat(
        widget.broadcastId,
        text,
      );
      _input.clear();
      _add(sent);
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              e.status == 429 ? '조금 천천히 보내 주세요.' : '채팅 전송 실패: ${e.message}',
            ),
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthController>();
    final scheme = Theme.of(context).colorScheme;
    return Container(
      height: widget.height,
      decoration: BoxDecoration(
        border: Border.all(color: scheme.outlineVariant),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
            child: Row(
              children: [
                Text('채팅', style: Theme.of(context).textTheme.titleSmall),
                const SizedBox(width: 6),
                Icon(
                  Icons.circle,
                  size: 8,
                  color: _connected ? Colors.green : scheme.outline,
                ),
                const Spacer(),
                Text(
                  _connected ? '실시간' : '연결 중…',
                  key: const Key('chat-state'),
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ],
            ),
          ),
          const Divider(height: 1),
          Expanded(
            child: _messages.isEmpty
                ? const Center(child: Text('첫 채팅을 보내 보세요.'))
                : ListView.builder(
                    controller: _scroll,
                    padding: const EdgeInsets.symmetric(
                      horizontal: 12,
                      vertical: 6,
                    ),
                    itemCount: _messages.length,
                    itemBuilder: (context, i) {
                      final m = _messages[i];
                      return Padding(
                        padding: const EdgeInsets.symmetric(vertical: 2),
                        child: Text.rich(
                          TextSpan(
                            children: [
                              TextSpan(
                                text: '${m.senderName}  ',
                                style: TextStyle(
                                  fontWeight: FontWeight.bold,
                                  color: m.senderId == auth.user?.id
                                      ? scheme.primary
                                      : scheme.tertiary,
                                ),
                              ),
                              TextSpan(text: m.text),
                            ],
                          ),
                        ),
                      );
                    },
                  ),
          ),
          const Divider(height: 1),
          Padding(
            padding: const EdgeInsets.fromLTRB(8, 4, 4, 4),
            child: auth.status == AuthStatus.authenticated
                ? Row(
                    children: [
                      Expanded(
                        child: TextField(
                          key: const Key('chat-input'),
                          controller: _input,
                          maxLength: 200,
                          decoration: const InputDecoration(
                            hintText: '채팅 입력',
                            counterText: '',
                            border: InputBorder.none,
                          ),
                          onSubmitted: (_) => _send(),
                        ),
                      ),
                      IconButton(
                        key: const Key('chat-send'),
                        tooltip: '보내기',
                        onPressed: _sending ? null : _send,
                        icon: const Icon(Icons.send),
                      ),
                    ],
                  )
                : Row(
                    children: [
                      const Expanded(child: Text('로그인하면 채팅할 수 있습니다.')),
                      TextButton(
                        key: const Key('chat-login'),
                        onPressed: () => Navigator.of(context).push(
                          MaterialPageRoute<void>(
                            builder: (_) => const LoginScreen(),
                          ),
                        ),
                        child: const Text('로그인'),
                      ),
                    ],
                  ),
          ),
        ],
      ),
    );
  }
}
