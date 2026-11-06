import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:wbb_frontend/api/wbb_api.dart';
import 'package:wbb_frontend/main.dart';

http.Response _json(Object body, [int status = 200]) => http.Response.bytes(
      utf8.encode(jsonEncode(body)),
      status,
      headers: {'content-type': 'application/json; charset=utf-8'},
    );

http.Response _unauthorized() => _json(
      {'error': {'code': 'unauthorized', 'message': 'Authentication required.'}},
      401,
    );

Map<String, dynamic> _broadcastJson(String id, String title) => {
      'id': id,
      'ownerId': 'u1',
      'owner': {'id': 'u1', 'displayName': 'Alice'},
      'title': title,
      'description': '설명',
      'version': 1,
      'status': 'ready',
      'currentSessionId': null,
      'createdAt': '2026-11-06T10:00:00Z',
      'updatedAt': '2026-11-06T10:00:00Z',
    };

Map<String, dynamic> _loginBody(String accessToken) => {
      'accessToken': accessToken,
      'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
    };

Widget _app(http.Client client) => WbbApp(apiClient: ApiClient(client: client));

void main() {
  testWidgets('visitor sees the public list without login', (tester) async {
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh')) return _unauthorized();
      if (request.method == 'GET' && request.url.path.endsWith('/broadcasts')) {
        return _json({
          'items': [_broadcastJson('b1', '첫 방송')],
          'nextCursor': '',
        });
      }
      fail('unexpected ${request.method} ${request.url.path}');
    });

    await tester.pumpWidget(_app(client));
    await tester.pumpAndSettle();

    expect(find.text('첫 방송'), findsOneWidget);
    expect(find.text('Alice · 2026.11.06'), findsOneWidget);
    expect(find.text('로그인'), findsOneWidget);
    expect(find.text('대기'), findsOneWidget);
  });

  testWidgets('login flow signs the user in and shows the display name',
      (tester) async {
    var loginCalls = 0;
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh')) return _unauthorized();
      if (request.url.path.endsWith('/auth/login')) {
        loginCalls++;
        return _json(_loginBody('at-1'));
      }
      if (request.method == 'GET' && request.url.path.endsWith('/broadcasts')) {
        return _json({'items': [], 'nextCursor': ''});
      }
      fail('unexpected ${request.method} ${request.url.path}');
    });

    await tester.pumpWidget(_app(client));
    await tester.pumpAndSettle();

    await tester.tap(find.text('로그인'));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('login-email')), 'a@b.c');
    await tester.enterText(find.byKey(const Key('login-password')), 'correct-horse-battery');
    await tester.tap(find.byKey(const Key('login-submit')));
    await tester.pumpAndSettle();

    expect(loginCalls, 1);
    expect(find.text('Alice'), findsOneWidget); // app bar shows the user
    expect(find.text('로그인'), findsNothing);
  });

  testWidgets('login failure shows the server message', (tester) async {
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh')) return _unauthorized();
      if (request.url.path.endsWith('/auth/login')) {
        return _json(
          {'error': {'code': 'invalid_credentials', 'message': 'Email or password is incorrect.'}},
          401,
        );
      }
      if (request.method == 'GET' && request.url.path.endsWith('/broadcasts')) {
        return _json({'items': [], 'nextCursor': ''});
      }
      fail('unexpected ${request.method} ${request.url.path}');
    });

    await tester.pumpWidget(_app(client));
    await tester.pumpAndSettle();

    await tester.tap(find.text('로그인'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('login-email')), 'a@b.c');
    await tester.enterText(find.byKey(const Key('login-password')), 'wrong-password');
    await tester.tap(find.byKey(const Key('login-submit')));
    await tester.pumpAndSettle();

    expect(find.text('Email or password is incorrect.'), findsOneWidget);
    expect(find.byKey(const Key('login-submit')), findsOneWidget); // stays on the form
  });

  testWidgets('signup validates input before any request', (tester) async {
    var tracking = false;
    final requests = <String>[];
    final client = MockClient((request) async {
      if (tracking) requests.add('${request.method} ${request.url.path}');
      if (request.method == 'GET' && request.url.path.endsWith('/broadcasts')) {
        return _json({'items': [], 'nextCursor': ''});
      }
      return _unauthorized();
    });

    await tester.pumpWidget(_app(client));
    await tester.pumpAndSettle();
    tracking = true; // startup restore is done; now watch the form

    await tester.tap(find.text('로그인'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('login-goto-signup')));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('signup-submit')));
    await tester.pumpAndSettle();

    expect(find.text('올바른 이메일을 입력하세요'), findsOneWidget);
    expect(find.text('비밀번호는 12자 이상이어야 합니다'), findsOneWidget);
    expect(find.text('표시 이름을 입력하세요'), findsOneWidget);
    expect(requests.where((r) => r.contains('/auth/register')), isEmpty);
  });

  testWidgets('authed user creates a broadcast through the form', (tester) async {
    String? createBody;
    String? createAuth;
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh')) return _json(_loginBody('at-1'));
      if (request.method == 'POST' && request.url.path.endsWith('/broadcasts')) {
        createBody = request.body;
        createAuth = request.headers['Authorization'];
        return _json({'broadcast': _broadcastJson('b2', '새 방송')}, 201);
      }
      if (request.url.path.endsWith('/auth/me')) {
        return _json({
          'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
        });
      }
      if (request.method == 'GET' && request.url.path.endsWith('/broadcasts')) {
        return _json({'items': [], 'nextCursor': ''});
      }
      fail('unexpected ${request.method} ${request.url.path}');
    });

    await tester.pumpWidget(_app(client));
    await tester.pumpAndSettle();
    expect(find.text('Alice'), findsOneWidget); // restored via refresh

    await tester.tap(find.byTooltip('내 방송'));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('my-broadcasts-create')));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('broadcast-title')), '새 방송');
    await tester.tap(find.byKey(const Key('broadcast-submit')));
    await tester.pumpAndSettle();

    expect(createBody, isNotNull);
    expect(jsonDecode(createBody!), {'title': '새 방송', 'description': ''});
    expect(createAuth, 'Bearer at-1');
  });

  testWidgets('logout returns the visitor state', (tester) async {
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh')) return _json(_loginBody('at-1'));
      if (request.url.path.endsWith('/auth/logout')) {
        return http.Response('', 204, headers: {'content-type': 'application/json'});
      }
      if (request.method == 'GET' && request.url.path.endsWith('/broadcasts')) {
        return _json({'items': [], 'nextCursor': ''});
      }
      fail('unexpected ${request.method} ${request.url.path}');
    });

    await tester.pumpWidget(_app(client));
    await tester.pumpAndSettle();
    expect(find.text('Alice'), findsOneWidget);

    await tester.tap(find.byTooltip('로그아웃'));
    await tester.pumpAndSettle();

    expect(find.text('로그인'), findsOneWidget);
  });
}
