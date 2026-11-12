import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/wbb_api.dart';
import '../models/donation.dart';
import '../payments/toss.dart';
import '../state/auth_controller.dart';
import 'home_screen.dart' show formatBroadcastDate;
import 'login_screen.dart';

String formatWon(int amount) {
  final digits = amount.toString();
  final out = StringBuffer();
  for (var i = 0; i < digits.length; i++) {
    if (i > 0 && (digits.length - i) % 3 == 0) out.write(',');
    out.write(digits[i]);
  }
  return '$out원';
}

/// Donations to the broadcast owner (payment provider test mode) with the
/// public history and total for this broadcast.
class DonationsPanel extends StatefulWidget {
  const DonationsPanel({super.key, required this.broadcastId});
  final String broadcastId;

  @override
  State<DonationsPanel> createState() => _DonationsPanelState();
}

class _DonationsPanelState extends State<DonationsPanel> {
  bool _enabled = false;
  DonationSummary? _summary;
  bool _starting = false;
  Timer? _poll;
  int _epoch = 0;

  @override
  void initState() {
    super.initState();
    _load();
    _poll = Timer.periodic(const Duration(seconds: 10), (_) => _load());
  }

  @override
  void dispose() {
    _poll?.cancel();
    _epoch++;
    super.dispose();
  }

  Future<void> _load() async {
    final epoch = ++_epoch;
    final api = context.read<ApiClient>();
    try {
      final enabled = await api.paymentsEnabled();
      final summary = enabled
          ? await api.listDonations(widget.broadcastId)
          : null;
      if (!mounted || epoch != _epoch) return;
      setState(() {
        _enabled = enabled;
        _summary = summary;
      });
    } on ApiException {
      // Donations are optional; hide the section on errors.
    }
  }

  Future<void> _donate() async {
    final auth = context.read<AuthController>();
    if (auth.status != AuthStatus.authenticated) {
      await Navigator.of(context)
          .push(MaterialPageRoute<void>(builder: (_) => const LoginScreen()));
      return;
    }
    final choice = await showDialog<(int, String)>(
      context: context,
      builder: (_) => const _DonationDialog(),
    );
    if (choice == null || !mounted) return;
    setState(() => _starting = true);
    try {
      final order = await context.read<ApiClient>().createDonation(
        widget.broadcastId,
        amount: choice.$1,
        message: choice.$2,
      );
      final origin = Uri.base.origin;
      final back = Uri.encodeQueryComponent(widget.broadcastId);
      await requestTossPayment(order.clientKey, {
        'orderId': order.orderId,
        'amount': order.amount,
        'orderName': order.orderName,
        'customerKey': order.customerKey,
        'successUrl': '$origin/donations/success?broadcastId=$back',
        'failUrl': '$origin/donations/fail?broadcastId=$back',
      });
    } on ApiException catch (e) {
      if (mounted) _toast('후원 요청 실패: ${e.message}');
    } catch (_) {
      if (mounted) _toast('결제창을 열지 못했거나 결제를 취소했습니다.');
    } finally {
      if (mounted) setState(() => _starting = false);
    }
  }

  void _toast(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    if (!_enabled) return const SizedBox.shrink();
    final summary = _summary;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Expanded(
              child: Text(
                summary == null
                    ? '후원'
                    : '후원 ${summary.count}건 · 합계 ${formatWon(summary.total)}',
                key: const Key('donation-total'),
                style: Theme.of(context).textTheme.titleMedium,
              ),
            ),
            FilledButton.icon(
              key: const Key('donation-start'),
              onPressed: _starting ? null : _donate,
              icon: const Icon(Icons.volunteer_activism_outlined),
              label: const Text('후원하기'),
            ),
          ],
        ),
        const SizedBox(height: 4),
        Text(
          '테스트 결제 모드입니다. 실제 금액이 청구되지 않습니다.',
          style: Theme.of(context).textTheme.bodySmall,
        ),
        if (summary != null)
          for (final d in summary.items.take(10))
            ListTile(
              dense: true,
              contentPadding: EdgeInsets.zero,
              leading: const Icon(Icons.favorite, color: Colors.pinkAccent),
              title: Text('${d.donorName} · ${formatWon(d.amount)}'),
              subtitle: Text(
                [
                  if (d.message.isNotEmpty) d.message,
                  formatBroadcastDate(d.confirmedAt),
                ].join(' · '),
              ),
            ),
      ],
    );
  }
}

class _DonationDialog extends StatefulWidget {
  const _DonationDialog();
  @override
  State<_DonationDialog> createState() => _DonationDialogState();
}

class _DonationDialogState extends State<_DonationDialog> {
  static const _presets = [1000, 5000, 10000, 50000];
  int _amount = 5000;
  final _custom = TextEditingController();
  final _message = TextEditingController();

  @override
  void dispose() {
    _custom.dispose();
    _message.dispose();
    super.dispose();
  }

  int? get _value {
    final typed = int.tryParse(_custom.text.replaceAll(',', '').trim());
    final v = _custom.text.trim().isEmpty ? _amount : typed;
    if (v == null || v < 1000 || v > 1000000) return null;
    return v;
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('후원하기'),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final p in _presets)
                  ChoiceChip(
                    label: Text(formatWon(p)),
                    selected: _custom.text.trim().isEmpty && _amount == p,
                    onSelected: (_) => setState(() {
                      _amount = p;
                      _custom.clear();
                    }),
                  ),
              ],
            ),
            TextField(
              key: const Key('donation-amount'),
              controller: _custom,
              keyboardType: TextInputType.number,
              decoration: const InputDecoration(
                labelText: '직접 입력 (1,000 ~ 1,000,000원)',
              ),
              onChanged: (_) => setState(() {}),
            ),
            TextField(
              key: const Key('donation-message'),
              controller: _message,
              maxLength: 200,
              decoration: const InputDecoration(labelText: '응원 메시지 (선택)'),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('취소'),
        ),
        FilledButton(
          key: const Key('donation-pay'),
          onPressed: _value == null
              ? null
              : () => Navigator.pop(context, (_value!, _message.text.trim())),
          child: Text(_value == null ? '금액 확인' : '${formatWon(_value!)} 결제'),
        ),
      ],
    );
  }
}
