import 'dart:convert';
import 'dart:math';

/// Request key for a new session attempt. The caller keeps it across retries.
String createSessionStartKey(Random random) =>
    's-${base64UrlEncode(List<int>.generate(18, (_) => random.nextInt(256)))}';
