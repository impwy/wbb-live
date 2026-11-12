import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../google_navigation.dart';
import '../state/auth_controller.dart';
import 'signup_screen.dart';

class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key, this.onGoogleStart, this.onLoginSuccess});
  final VoidCallback? onGoogleStart;

  /// When this screen REPLACED another route (e.g. the Google callback
  /// screen), a plain pop would leave an empty navigator. The replacer
  /// passes a destination here instead.
  final VoidCallback? onLoginSuccess;

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final _formKey = GlobalKey<FormState>();
  final _email = TextEditingController();
  final _password = TextEditingController();
  bool _busy = false;
  String? _error;
  bool _googleEnabled = false;
  bool _evaluationEnabled = false;

  @override
  void initState() {
    super.initState();
    _loadProviders();
  }

  Future<void> _loadProviders() async {
    try {
      final providers = await context.read<ApiClient>().authProviders();
      if (mounted) {
        setState(() {
          _googleEnabled = providers.googleEnabled;
          _evaluationEnabled = providers.evaluationEnabled;
        });
      }
    } on ApiException {
      if (mounted) {
        setState(() {
          _googleEnabled = false;
          _evaluationEnabled = false;
        });
      }
    }
  }

  @override
  void dispose() {
    _email.dispose();
    _password.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    await _completeLogin(
      () => context.read<AuthController>().login(
        _email.text.trim(),
        _password.text,
      ),
    );
  }

  Future<void> _completeLogin(Future<void> Function() login) async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await login();
      if (!mounted) return;
      if (widget.onLoginSuccess != null) {
        widget.onLoginSuccess!();
      } else {
        Navigator.of(context).pop();
      }
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('로그인')),
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
                    key: const Key('login-email'),
                    controller: _email,
                    decoration: const InputDecoration(labelText: '이메일'),
                    keyboardType: TextInputType.emailAddress,
                    validator: (v) =>
                        v == null || !v.contains('@') ? '올바른 이메일을 입력하세요' : null,
                  ),
                  const SizedBox(height: 12),
                  TextFormField(
                    key: const Key('login-password'),
                    controller: _password,
                    decoration: const InputDecoration(labelText: '비밀번호'),
                    obscureText: true,
                    validator: (v) =>
                        v == null || v.isEmpty ? '비밀번호를 입력하세요' : null,
                  ),
                  if (_error != null)
                    Padding(
                      padding: const EdgeInsets.only(top: 12),
                      child: Text(
                        _error!,
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
                    ),
                  const SizedBox(height: 20),
                  FilledButton(
                    key: const Key('login-submit'),
                    onPressed: _busy ? null : _submit,
                    child: _busy
                        ? const SizedBox(
                            height: 18,
                            width: 18,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Text('로그인'),
                  ),
                  if (_googleEnabled) ...[
                    const SizedBox(height: 12),
                    OutlinedButton(
                      key: const Key('login-google'),
                      onPressed: _busy
                          ? null
                          : (widget.onGoogleStart ??
                                () => startGoogleLogin(
                                  context.read<ApiClient>().googleStartUri,
                                )),
                      child: const Text('Google로 로그인'),
                    ),
                  ],
                  if (_evaluationEnabled) ...[
                    const SizedBox(height: 12),
                    OutlinedButton(
                      key: const Key('login-evaluation'),
                      onPressed: _busy
                          ? null
                          : () => _completeLogin(
                              context.read<AuthController>().evaluationLogin,
                            ),
                      child: const Text('평가용 바로 로그인'),
                    ),
                  ],
                  TextButton(
                    key: const Key('login-goto-signup'),
                    onPressed: () => Navigator.of(context).push(
                      MaterialPageRoute<void>(
                        // Forward the destination when this LoginScreen
                        // REPLACED a route, so signup success also lands in
                        // the app instead of stripping the last route.
                        builder: (_) => SignupScreen(
                          onSignupSuccess: widget.onLoginSuccess,
                        ),
                      ),
                    ),
                    child: const Text('계정이 없나요? 가입하기'),
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
