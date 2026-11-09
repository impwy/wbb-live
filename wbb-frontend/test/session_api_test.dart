import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:wbb_frontend/api/wbb_api.dart';

void main() {
  test(
    'prepare retry keeps its idempotency key after access refresh',
    () async {
      final seenKeys = <String>[];
      final client = ApiClient(
        client: MockClient((request) async {
          if (request.url.path.endsWith('/auth/login')) {
            return http.Response(
              jsonEncode({
                'accessToken': 'old',
                'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
              }),
              200,
            );
          }
          if (request.url.path.endsWith('/auth/refresh')) {
            return http.Response(
              jsonEncode({
                'accessToken': 'new',
                'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
              }),
              200,
            );
          }
          if (request.url.path.endsWith('/broadcasts/b1/sessions')) {
            seenKeys.add(request.headers['Idempotency-Key'] ?? '');
            if (request.headers['Authorization'] == 'Bearer old') {
              return http.Response(
                jsonEncode({
                  'error': {'code': 'unauthorized', 'message': 'expired'},
                }),
                401,
              );
            }
            return http.Response(
              jsonEncode({
                'session': {
                  'id': 's1',
                  'broadcastId': 'b1',
                  'state': 'starting',
                  'version': 1,
                  'startedAt': '2026-11-09T01:00:00Z',
                  'endedAt': null,
                },
                'publishGrant': null,
              }),
              202,
            );
          }
          fail('unexpected ${request.method} ${request.url.path}');
        }),
      );
      await client.login('a@b.c', 'pw');
      final session = await client.prepareSession(
        'b1',
        source: 'screen',
        idempotencyKey: 'stable-key-0001',
      );
      expect(session.state, 'starting');
      expect(seenKeys, ['stable-key-0001', 'stable-key-0001']);
    },
  );
}
