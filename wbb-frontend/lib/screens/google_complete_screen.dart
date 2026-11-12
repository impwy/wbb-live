import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../state/auth_controller.dart';
import 'login_screen.dart';
import 'home_screen.dart';

/// Callback landing screen. AuthController.restore is already running from
/// WbbApp; this screen never calls refresh a second time.
class GoogleCompleteScreen extends StatefulWidget {
  const GoogleCompleteScreen({super.key, required this.error});
  final String? error;

  @override
  State<GoogleCompleteScreen> createState() => _GoogleCompleteScreenState();
}

class _GoogleCompleteScreenState extends State<GoogleCompleteScreen> {
  bool _navigated = false;

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthController>();
    if (widget.error == null &&
        auth.status == AuthStatus.authenticated &&
        !_navigated) {
      _navigated = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) {
          // Home becomes the new ROOT: no auth-flow route may remain below.
          Navigator.of(context).pushAndRemoveUntil(
            MaterialPageRoute<void>(
              builder: (_) => const HomeScreen(openMyBroadcastsOnLoad: true),
            ),
            (route) => false,
          );
        }
      });
    }
    final message = switch (widget.error) {
      'cancelled' => 'Google 로그인이 취소되었습니다.',
      'account_exists' => '이미 같은 이메일의 계정이 있습니다. 기존 방법으로 로그인해 주세요.',
      'invalid_request' => '로그인 요청이 만료되거나 유효하지 않습니다. 다시 시도해 주세요.',
      'unavailable' => 'Google 로그인을 완료하지 못했습니다. 다시 시도해 주세요.',
      null when auth.status == AuthStatus.unauthenticated =>
        '로그인 상태를 확인하지 못했습니다. 다시 시도해 주세요.',
      _ => widget.error == null ? null : '로그인 요청을 확인할 수 없습니다. 다시 시도해 주세요.',
    };
    return Scaffold(
      appBar: AppBar(title: const Text('Google 로그인')),
      body: Center(
        child: message == null
            ? const CircularProgressIndicator()
            : Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(message, textAlign: TextAlign.center),
                  const SizedBox(height: 16),
                  TextButton(
                    // Capture the NavigatorState BEFORE replacing this screen:
                    // the closure must not touch this (soon unmounted) context.
                    onPressed: () {
                      final nav = Navigator.of(context);
                      // The whole auth flow (Login/Signup pushed from here) is
                      // removed on success: Home becomes the new ROOT so platform
                      // Back can never resurface a login form.
                      void goToHome() => nav.pushAndRemoveUntil(
                        MaterialPageRoute<void>(
                          builder: (_) =>
                              const HomeScreen(openMyBroadcastsOnLoad: true),
                        ),
                        (route) => false,
                      );
                      nav.pushReplacement(
                        MaterialPageRoute<void>(
                          builder: (_) => LoginScreen(onLoginSuccess: goToHome),
                        ),
                      );
                    },
                    child: const Text('로그인 다시 시도'),
                  ),
                ],
              ),
      ),
    );
  }
}
