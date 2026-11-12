import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';
import 'package:wbb_frontend/api/wbb_api.dart';
import 'package:wbb_frontend/screens/login_screen.dart';
import 'package:wbb_frontend/state/auth_controller.dart';

http.Response jsonReply(Object body, [int status = 200]) => http.Response(
  jsonEncode(body),
  status,
  headers: {'content-type': 'application/json'},
);

Future<void> showLogin(
  WidgetTester tester,
  ApiClient api,
  AuthController auth, {
  VoidCallback? success,
}) async {
  await tester.pumpWidget(
    MultiProvider(
      providers: [
        Provider<ApiClient>.value(value: api),
        ChangeNotifierProvider<AuthController>.value(value: auth),
      ],
      child: MaterialApp(home: LoginScreen(onLoginSuccess: success)),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  test(
    'late evaluation response cannot restore a logged-out session',
    () async {
      final pending = Completer<http.Response>();
      final api = ApiClient(
        client: MockClient((request) async {
          if (request.url.path.endsWith('/auth/evaluation-login')) {
            return pending.future;
          }
          if (request.url.path.endsWith('/auth/logout')) {
            return http.Response('', 204);
          }
          fail('unexpected ${request.url.path}');
        }),
      );
      final auth = AuthController(api);
      final login = auth.evaluationLogin();
      await auth.logout();
      pending.complete(
        jsonReply({
          'accessToken': 'late',
          'user': {
            'id': 'late',
            'email': 'late@example.test',
            'displayName': 'Late',
          },
        }),
      );
      await expectLater(login, throwsA(isA<ApiException>()));
      expect(auth.status, AuthStatus.unauthenticated);
      expect(api.hasAccessToken, isFalse);
    },
  );

  test(
    'late logout completion cannot clear a newer evaluation login',
    () async {
      final pendingLogout = Completer<http.Response>();
      final api = ApiClient(
        client: MockClient((request) async {
          if (request.url.path.endsWith('/auth/login')) {
            return jsonReply({
              'accessToken': 'old',
              'user': {
                'id': 'old',
                'email': 'old@example.test',
                'displayName': 'Old',
              },
            });
          }
          if (request.url.path.endsWith('/auth/logout')) {
            return pendingLogout.future;
          }
          if (request.url.path.endsWith('/auth/evaluation-login')) {
            return jsonReply({
              'accessToken': 'new',
              'user': {
                'id': 'new',
                'email': 'new@example.test',
                'displayName': 'New',
              },
            });
          }
          fail('unexpected ${request.url.path}');
        }),
      );
      final auth = AuthController(api);
      await auth.login('old@example.test', 'placeholder');
      final logout = auth.logout();
      final login = auth.evaluationLogin();
      pendingLogout.complete(http.Response('', 204));
      await logout;
      await login;
      expect(auth.status, AuthStatus.authenticated);
      expect(auth.user?.id, 'new');
      expect(api.hasAccessToken, isTrue);
    },
  );

  test(
    'superseded evaluation intent never sends a late login request',
    () async {
      final pendingLogout = Completer<http.Response>();
      var evaluationPosts = 0;
      final api = ApiClient(
        client: MockClient((request) async {
          if (request.url.path.endsWith('/auth/login')) {
            return jsonReply({
              'accessToken': 'old',
              'user': {
                'id': 'old',
                'email': 'old@example.test',
                'displayName': 'Old',
              },
            });
          }
          if (request.url.path.endsWith('/auth/logout')) {
            return pendingLogout.future;
          }
          if (request.url.path.endsWith('/auth/evaluation-login')) {
            evaluationPosts++;
            return jsonReply({
              'accessToken': 'new',
              'user': {
                'id': 'new',
                'email': 'new@example.test',
                'displayName': 'New',
              },
            });
          }
          fail('unexpected ${request.url.path}');
        }),
      );
      final auth = AuthController(api);
      await auth.login('old@example.test', 'placeholder');
      final logout = auth.logout();
      final oldIntent = auth.evaluationLogin();
      final oldFailure = expectLater(oldIntent, throwsA(isA<ApiException>()));
      final newIntent = auth.evaluationLogin();
      pendingLogout.complete(http.Response('', 204));
      await logout;
      await oldFailure;
      await newIntent;
      expect(evaluationPosts, 1);
      expect(auth.user?.id, 'new');
    },
  );

  testWidgets('evaluation button is absent on disabled or failing providers', (
    tester,
  ) async {
    for (final status in [200, 503]) {
      final api = ApiClient(
        client: MockClient(
          (request) async => jsonReply({
            'googleEnabled': false,
            'evaluationEnabled': false,
          }, status),
        ),
      );
      await showLogin(tester, api, AuthController(api));
      expect(find.byKey(const Key('login-evaluation')), findsNothing);
      await tester.pumpWidget(const SizedBox());
    }
  });

  testWidgets(
    'evaluation button uses normal auth state and ignores duplicate tap',
    (tester) async {
      final pending = Completer<http.Response>();
      var posts = 0;
      var success = 0;
      final api = ApiClient(
        client: MockClient((request) async {
          if (request.url.path.endsWith('/auth/providers')) {
            return jsonReply({
              'googleEnabled': true,
              'evaluationEnabled': true,
            });
          }
          if (request.url.path.endsWith('/auth/evaluation-login')) {
            posts++;
            expect(request.body, isEmpty);
            return pending.future;
          }
          fail('unexpected ${request.url.path}');
        }),
      );
      final auth = AuthController(api);
      await showLogin(tester, api, auth, success: () => success++);
      final button = find.byKey(const Key('login-evaluation'));
      expect(button, findsOneWidget);
      expect(find.byKey(const Key('login-google')), findsOneWidget);
      await tester.tap(button);
      await tester.pump();
      expect(tester.widget<ButtonStyleButton>(button).onPressed, isNull);
      expect(posts, 1);
      pending.complete(
        jsonReply({
          'accessToken': 'memory-token',
          'user': {
            'id': 'eval',
            'email': 'eval@example.test',
            'displayName': 'Evaluator',
          },
        }),
      );
      await tester.pumpAndSettle();
      expect(auth.status, AuthStatus.authenticated);
      expect(auth.user?.id, 'eval');
      expect(api.hasAccessToken, isTrue);
      expect(success, 1);
    },
  );

  testWidgets('failed evaluation login keeps existing signed-in state', (
    tester,
  ) async {
    final api = ApiClient(
      client: MockClient((request) async {
        if (request.url.path.endsWith('/auth/providers')) {
          return jsonReply({'googleEnabled': false, 'evaluationEnabled': true});
        }
        if (request.url.path.endsWith('/auth/login')) {
          return jsonReply({
            'accessToken': 'original',
            'user': {
              'id': 'original',
              'email': 'old@example.test',
              'displayName': 'Old',
            },
          });
        }
        if (request.url.path.endsWith('/auth/evaluation-login')) {
          return jsonReply({
            'error': {'code': 'invalid_credentials', 'message': 'Unavailable'},
          }, 401);
        }
        fail('unexpected ${request.url.path}');
      }),
    );
    final auth = AuthController(api);
    await auth.login('old@example.test', 'placeholder');
    await showLogin(tester, api, auth);
    await tester.tap(find.byKey(const Key('login-evaluation')));
    await tester.pumpAndSettle();
    expect(auth.status, AuthStatus.authenticated);
    expect(auth.user?.id, 'original');
    expect(api.hasAccessToken, isTrue);
    expect(find.text('Unavailable'), findsOneWidget);
  });
}
