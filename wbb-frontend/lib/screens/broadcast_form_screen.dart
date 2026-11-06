import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../models/broadcast.dart';

/// Create or edit a broadcast's metadata (title/description, version-guarded).
/// On a version conflict (409) the user can reload the latest values and
/// retry without losing the form.
class BroadcastFormScreen extends StatefulWidget {
  const BroadcastFormScreen({super.key, this.existing});

  final Broadcast? existing;

  @override
  State<BroadcastFormScreen> createState() => _BroadcastFormScreenState();
}

class _BroadcastFormScreenState extends State<BroadcastFormScreen> {
  final _formKey = GlobalKey<FormState>();
  late final _title = TextEditingController(text: widget.existing?.title ?? '');
  late final _description = TextEditingController(text: widget.existing?.description ?? '');

  /// Latest known server state when editing; bumped by reload after 409.
  Broadcast? _latest;

  bool _busy = false;
  String? _error;
  bool _conflict = false;

  int get _version => _latest?.version ?? widget.existing?.version ?? 0;

  @override
  void dispose() {
    _title.dispose();
    _description.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final api = context.read<ApiClient>();
      if (widget.existing == null) {
        await api.createBroadcast(title: _title.text.trim(), description: _description.text.trim());
      } else {
        await api.updateBroadcast(
          widget.existing!.id,
          version: _version,
          title: _title.text.trim(),
          description: _description.text.trim(),
        );
      }
      if (mounted) Navigator.of(context).pop(true);
    } on ApiException catch (e) {
      setState(() {
        _conflict = e.code == 'version_conflict';
        _error = _conflict ? '다른 곳에서 수정되었습니다. 최신 내용을 불러온 뒤 다시 시도할 수 있어요.' : e.message;
      });
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _reloadLatest() async {
    try {
      final latest = await context.read<ApiClient>().getBroadcast(widget.existing!.id);
      if (!mounted) return;
      setState(() {
        _latest = latest;
        _title.text = latest.title;
        _description.text = latest.description;
        _conflict = false;
        _error = null;
      });
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = '불러오기 실패: ${e.message}');
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(widget.existing == null ? '방송 등록' : '방송 수정')),
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 420),
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Form(
              key: _formKey,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TextFormField(
                    key: const Key('broadcast-title'),
                    controller: _title,
                    decoration: InputDecoration(
                      labelText: widget.existing == null ? '제목 (최대 100자)' : '제목 (버전 $_version)',
                    ),
                    validator: (v) {
                      final t = v?.trim() ?? '';
                      if (t.isEmpty) return '제목을 입력하세요';
                      if (t.length > 100) return '100자 이하로 입력하세요';
                      return null;
                    },
                  ),
                  const SizedBox(height: 12),
                  TextFormField(
                    key: const Key('broadcast-description'),
                    controller: _description,
                    decoration: const InputDecoration(labelText: '설명 (선택, 최대 2000자)'),
                    maxLines: 4,
                    validator: (v) =>
                        (v?.length ?? 0) > 2000 ? '2000자 이하로 입력하세요' : null,
                  ),
                  if (_error != null)
                    Padding(
                      padding: const EdgeInsets.only(top: 12),
                      child: Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
                    ),
                  if (_conflict) ...[
                    const SizedBox(height: 8),
                    OutlinedButton.icon(
                      key: const Key('broadcast-reload'),
                      onPressed: _reloadLatest,
                      icon: const Icon(Icons.refresh),
                      label: const Text('최신 내용 불러오기'),
                    ),
                  ],
                  const SizedBox(height: 20),
                  FilledButton(
                    key: const Key('broadcast-submit'),
                    onPressed: _busy ? null : _submit,
                    child: _busy
                        ? const SizedBox(height: 18, width: 18, child: CircularProgressIndicator(strokeWidth: 2))
                        : Text(widget.existing == null ? '등록' : '수정'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
