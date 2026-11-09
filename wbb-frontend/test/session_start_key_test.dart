import 'dart:math';

import 'package:flutter_test/flutter_test.dart';
import 'package:wbb_frontend/api/session_start_key.dart';

void main() {
  test('generated keys always satisfy the server Idempotency-Key format', () {
    final random = Random(0);
    final accepted = RegExp(r'^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$');
    for (var i = 0; i < 256; i++) {
      expect(createSessionStartKey(random), matches(accepted));
    }
  });
}
