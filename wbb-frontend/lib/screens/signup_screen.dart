import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../state/auth_controller.dart';

class SignupScreen extends StatefulWidget {
  const SignupScreen({super.key});

  @override
  State<SignupScreen> createState() => _SignupScreenState();
}

class _SignupScreenState extends State<SignupScreen> {
  final _formKey = GlobalKey<FormState>();
  final _email = TextEditingController();
  final _password = TextEditingController();
  final _displayName = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _email.dispose();
    _password.dispose();
    _displayName.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await context.read<AuthController>().register(
            _email.text.trim(),
            _password.text,
            _displayName.text.trim(),
          );
      if (mounted) {
        Navigator.of(context)
          ..pop()
          ..pop();
      }
    } on ApiException catch (e) {
      setState(() => _error = e.code == 'email_taken' ? '이미 등록된 이메일입니다.' : e.message);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('가입')),
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
                    key: const Key('signup-email'),
                    controller: _email,
                    decoration: const InputDecoration(labelText: '이메일'),
                    keyboardType: TextInputType.emailAddress,
                    validator: (v) =>
                        v == null || !v.contains('@') ? '올바른 이메일을 입력하세요' : null,
                  ),
                  const SizedBox(height: 12),
                  TextFormField(
                    key: const Key('signup-password'),
                    controller: _password,
                    decoration: const InputDecoration(labelText: '비밀번호 (12자 이상)'),
                    obscureText: true,
                    validator: (v) =>
                        v == null || v.length < 12 ? '비밀번호는 12자 이상이어야 합니다' : null,
                  ),
                  const SizedBox(height: 12),
                  TextFormField(
                    key: const Key('signup-display-name'),
                    controller: _displayName,
                    decoration: const InputDecoration(labelText: '표시 이름 (최대 40자)'),
                    validator: (v) {
                      if (v == null || v.trim().isEmpty) return '표시 이름을 입력하세요';
                      if (v.trim().length > 40) return '40자 이하로 입력하세요';
                      return null;
                    },
                  ),
                  if (_error != null)
                    Padding(
                      padding: const EdgeInsets.only(top: 12),
                      child: Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
                    ),
                  const SizedBox(height: 20),
                  FilledButton(
                    key: const Key('signup-submit'),
                    onPressed: _busy ? null : _submit,
                    child: _busy
                        ? const SizedBox(height: 18, width: 18, child: CircularProgressIndicator(strokeWidth: 2))
                        : const Text('가입하고 로그인'),
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
