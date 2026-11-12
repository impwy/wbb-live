import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:wbb_frontend/api/wbb_api.dart';

void main() {
  group('ApiClient', () {
    test('login parses tokens and user', () async {
      late String path;
      final client = ApiClient(client: MockClient((request) async {
        path = request.url.path;
        return http.Response(
          jsonEncode({
            'accessToken': 'at-1',
            'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
          }),
          200,
        );
      }));
      final result = await client.login('a@b.c', 'pw');
      expect(path, endsWith('/auth/login'));
      expect(result.accessToken, 'at-1');
      expect(result.user.displayName, 'Alice');
    });

    test('surfaces the error envelope', () async {
      final client = ApiClient(client: MockClient((request) async {
        return http.Response(
          jsonEncode({
            'error': {'code': 'invalid_credentials', 'message': 'Email or password is incorrect.'},
          }),
          401,
        );
      }));
      await expectLater(
        client.login('a@b.c', 'wrong'),
        throwsA(isA<ApiException>().having((e) => e.code, 'code', 'invalid_credentials')),
      );
    });

    test('401 on an authenticated call triggers one refresh and retries once',
        () async {
      var refreshCalls = 0;
      var meCalls = 0;
      late String retryBearer;
      final client = ApiClient(client: MockClient((request) async {
        if (request.url.path.endsWith('/auth/login')) {
          return http.Response(
            jsonEncode({
              'accessToken': 'at-1',
              'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
            }),
            200,
          );
        }
        if (request.url.path.endsWith('/auth/refresh')) {
          refreshCalls++;
          return http.Response(
            jsonEncode({
              'accessToken': 'at-2',
              'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
            }),
            200,
          );
        }
        if (request.url.path.endsWith('/auth/me')) {
          meCalls++;
          final bearer = request.headers['Authorization'] ?? '';
          if (bearer.endsWith('at-1')) {
            return http.Response(jsonEncode({'error': {'code': 'unauthorized', 'message': 'x'}}), 401);
          }
          retryBearer = bearer;
          return http.Response(
            jsonEncode({
              'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
            }),
            200,
          );
        }
        fail('unexpected request ${request.url.path}');
      }));
      await client.login('a@b.c', 'pw'); // sets access token at-1
      await client.me();
      expect(refreshCalls, 1);
      expect(meCalls, 2);
      expect(retryBearer, endsWith('at-2'));
    });

    test('concurrent 401 retries share one single-flight refresh', () async {
      var refreshCalls = 0;
      final client = ApiClient(client: MockClient((request) async {
        if (request.url.path.endsWith('/auth/login')) {
          return http.Response(
            jsonEncode({
              'accessToken': 'at-1',
              'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
            }),
            200,
          );
        }
        if (request.url.path.endsWith('/auth/refresh')) {
          refreshCalls++;
          return http.Response(
            jsonEncode({
              'accessToken': 'at-2',
              'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
            }),
            200,
          );
        }
        // Every authenticated call 401s once, then succeeds.
        final bearer = request.headers['Authorization'] ?? '';
        if (bearer.endsWith('at-1')) {
          return http.Response(jsonEncode({'error': {'code': 'unauthorized', 'message': 'x'}}), 401);
        }
        return http.Response(
          jsonEncode({
            'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
          }),
          200,
        );
      }));
      await client.login('a@b.c', 'pw');
      await Future.wait([client.me(), client.me(), client.me()]);
      expect(refreshCalls, 1);
    });
  });
}
