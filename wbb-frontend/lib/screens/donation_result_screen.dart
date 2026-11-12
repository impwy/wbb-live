import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../state/auth_controller.dart';
import 'broadcast_detail_screen.dart';
import 'donations_panel.dart' show formatWon;
import 'home_screen.dart';

/// Landing page after the payment provider redirects back. On success the
/// server verifies the payment against the stored order before crediting.
class DonationResultScreen extends StatefulWidget {
  const DonationResultScreen({super.key, required this.uri});
  final Uri uri;

  @override
  State<DonationResultScreen> createState() => _DonationResultScreenState();
}

class _DonationResultScreenState extends State<DonationResultScreen> {
  String _message = '결제를 확인하는 중입니다…';
  bool _done = false;
  bool _ok = false;
  bool _started = false;

  bool get _success => widget.uri.path == '/donations/success';

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final auth = context.watch<AuthController>();
    if (_started || auth.status == AuthStatus.unknown) return;
    _started = true;
    _confirm(auth);
  }

  Future<void> _confirm(AuthController auth) async {
    final q = widget.uri.queryParameters;
    if (!_success) {
      setState(() {
        _done = true;
        _message = '결제가 완료되지 않았습니다. ${q['message'] ?? ''}'.trim();
      });
      return;
    }
    if (auth.status != AuthStatus.authenticated) {
      setState(() {
        _done = true;
        _message = '로그인이 만료되어 결제를 확인하지 못했습니다. 다시 로그인한 뒤 후원해 주세요.';
      });
      return;
    }
    final amount = int.tryParse(q['amount'] ?? '');
    try {
      final credited = await context.read<ApiClient>().confirmDonation(
        paymentKey: q['paymentKey'] ?? '',
        orderId: q['orderId'] ?? '',
        amount: amount ?? -1,
      );
      if (!mounted) return;
      setState(() {
        _done = true;
        _ok = true;
        _message = credited
            ? '${formatWon(amount ?? 0)} 후원이 완료되었습니다. 감사합니다!'
            : '이미 처리된 후원입니다. (중복 적립 없음)';
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _done = true;
        _message = '후원 확인 실패: ${e.message}';
      });
    }
  }

  void _back() {
    final id = widget.uri.queryParameters['broadcastId'];
    final nav = Navigator.of(context);
    nav.pushAndRemoveUntil(
      MaterialPageRoute<void>(builder: (_) => const HomeScreen()),
      (_) => false,
    );
    if (id != null && id.isNotEmpty) {
      nav.push(
        MaterialPageRoute<void>(
          builder: (_) => BroadcastDetailScreen(broadcastId: id),
        ),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('후원 결과')),
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (!_done)
                const CircularProgressIndicator()
              else
                Icon(
                  _ok ? Icons.check_circle : Icons.error_outline,
                  size: 56,
                  color: _ok ? Colors.green : Colors.orange,
                ),
              const SizedBox(height: 16),
              Text(
                _message,
                key: const Key('donation-result'),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 24),
              if (_done)
                FilledButton(
                  key: const Key('donation-back'),
                  onPressed: _back,
                  child: const Text('방송으로 돌아가기'),
                ),
            ],
          ),
        ),
      ),
    );
  }
}
