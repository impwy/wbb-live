import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';
import 'package:wbb_frontend/api/wbb_api.dart';
import 'package:wbb_frontend/main.dart';
import 'package:wbb_frontend/screens/home_screen.dart';
import 'package:wbb_frontend/screens/login_screen.dart';
import 'package:wbb_frontend/screens/signup_screen.dart';

http.Response reply(Object value, [int status = 200]) => http.Response(
  jsonEncode(value),
  status,
  headers: {'content-type': 'application/json'},
);

Future<void> expectRootHomeSurvivesPlatformBack(WidgetTester tester) async {
  expect(find.byType(HomeScreen), findsOneWidget);
  expect(
    Navigator.of(tester.element(find.byType(HomeScreen))).canPop(),
    isFalse,
  );
  expect(find.byType(LoginScreen, skipOffstage: false), findsNothing);
  expect(find.byType(SignupScreen, skipOffstage: false), findsNothing);
  await tester.binding.handlePopRoute();
  await tester.pumpAndSettle();
  expect(find.byType(HomeScreen), findsOneWidget);
  expect(find.byTooltip('내 방송'), findsOneWidget);
  expect(
    Navigator.of(tester.element(find.byType(HomeScreen))).canPop(),
    isFalse,
  );
  expect(find.byType(LoginScreen, skipOffstage: false), findsNothing);
}

void main() {
  test('Google start navigation uses the configured API origin', () {
    final api = ApiClient(
      baseUrl: 'http://127.0.0.1:8080/api/v1',
      client: MockClient((_) async => reply({})),
    );
    expect(
      api.googleStartUri.toString(),
      'http://127.0.0.1:8080/api/v1/auth/google/start',
    );
  });
  testWidgets('Google button is shown only when the provider is enabled', (
    tester,
  ) async {
    var starts = 0;
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/providers')) {
        return reply({'googleEnabled': true});
      }
      fail('unexpected ${request.url}');
    });
    await tester.pumpWidget(
      Provider<ApiClient>.value(
        value: ApiClient(client: client),
        child: MaterialApp(home: LoginScreen(onGoogleStart: () => starts++)),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('login-google')));
    expect(starts, 1);
  });

  testWidgets('Google button is absent when provider is disabled', (
    tester,
  ) async {
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/providers')) {
        return reply({'googleEnabled': false});
      }
      fail('unexpected ${request.url}');
    });
    await tester.pumpWidget(
      Provider<ApiClient>.value(
        value: ApiClient(client: client),
        child: const MaterialApp(home: LoginScreen()),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('login-google')), findsNothing);
  });

  testWidgets('direct callback restores once and opens broadcast management', (
    tester,
  ) async {
    var refreshes = 0;
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh')) {
        refreshes++;
        return reply({
          'accessToken': 'in-memory-only',
          'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
        });
      }
      if (request.url.path.endsWith('/auth/me')) {
        return reply({
          'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
        });
      }
      if (request.url.path.endsWith('/broadcasts')) {
        return reply({'items': [], 'nextCursor': ''});
      }
      if (request.url.path.endsWith('/auth/logout')) {
        return http.Response('', 204);
      }
      fail('unexpected ${request.url}');
    });
    await tester.pumpWidget(
      WbbApp(
        apiClient: ApiClient(client: client),
        initialUri: Uri.parse('https://example.com/auth/google/complete'),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('내 방송'), findsOneWidget);
    expect(refreshes, 1);
    await tester.pageBack();
    await tester.pumpAndSettle();
    await expectRootHomeSurvivesPlatformBack(tester);
    await tester.tap(find.byTooltip('로그아웃'));
    await tester.pumpAndSettle();
    expect(find.text('로그인'), findsOneWidget);
  });

  testWidgets('cancelled callback shows retry without claiming success', (
    tester,
  ) async {
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh')) {
        return reply({
          'error': {
            'code': 'unauthorized',
            'message': 'Authentication required.',
          },
        }, 401);
      }
      if (request.url.path.endsWith('/auth/providers')) {
        return reply({'googleEnabled': true});
      }
      fail('unexpected ${request.url}');
    });
    await tester.pumpWidget(
      WbbApp(
        apiClient: ApiClient(client: client),
        initialUri: Uri.parse(
          'https://example.com/auth/google/complete?error=cancelled',
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.textContaining('취소되었습니다'), findsOneWidget);
    expect(find.text('내 방송'), findsNothing);
    await tester.tap(find.text('로그인 다시 시도'));
    await tester.pumpAndSettle();
    expect(find.byType(LoginScreen), findsOneWidget);
  });

  for (final errorCode in ['cancelled', 'account_exists']) {
    testWidgets('$errorCode → retry → email login success reaches home (F2)', (
      tester,
    ) async {
      final client = MockClient((request) async {
        if (request.url.path.endsWith('/auth/refresh')) {
          return reply({
            'error': {
              'code': 'unauthorized',
              'message': 'Authentication required.',
            },
          }, 401);
        }
        if (request.url.path.endsWith('/auth/providers')) {
          return reply({'googleEnabled': true});
        }
        if (request.url.path.endsWith('/auth/login')) {
          return reply({
            'accessToken': 'at-email',
            'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
          });
        }
        if (request.url.path.endsWith('/auth/me')) {
          return reply({
            'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
          });
        }
        if (request.url.path.endsWith('/broadcasts')) {
          return reply({'items': [], 'nextCursor': ''});
        }
        fail('unexpected ${request.url}');
      });
      await tester.pumpWidget(
        WbbApp(
          apiClient: ApiClient(client: client),
          initialUri: Uri.parse(
            'https://example.com/auth/google/complete?error=$errorCode',
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('로그인 다시 시도'));
      await tester.pumpAndSettle();
      await tester.enterText(find.byKey(const Key('login-email')), 'a@b.c');
      await tester.enterText(
        find.byKey(const Key('login-password')),
        'correct-horse-battery',
      );
      await tester.tap(find.byKey(const Key('login-submit')));
      await tester.pumpAndSettle();
      // Email success must leave the login flow and reach the app — never a
      // blank route left behind by replacing the callback screen. Home opens
      // broadcast management directly (openMyBroadcastsOnLoad).
      expect(find.byType(LoginScreen), findsNothing);
      expect(find.text('로그인 다시 시도'), findsNothing);
      expect(find.text('등록한 방송이 없습니다. + 버튼으로 만드세요.'), findsOneWidget);
      expect(find.byKey(const Key('my-broadcasts-create')), findsOneWidget);
      // Popping the management screen still lands on a working Home.
      await tester.pageBack();
      await tester.pumpAndSettle();
      expect(find.text('Alice'), findsOneWidget);
      expect(find.byTooltip('내 방송'), findsOneWidget);
      await expectRootHomeSurvivesPlatformBack(tester);
    });
  }

  for (final errorCode in ['cancelled', 'account_exists']) {
    testWidgets(
      '$errorCode → retry → signup success reaches the app (F2 signup path)',
      (tester) async {
        final client = MockClient((request) async {
          if (request.url.path.endsWith('/auth/refresh')) {
            return reply({
              'error': {
                'code': 'unauthorized',
                'message': 'Authentication required.',
              },
            }, 401);
          }
          if (request.url.path.endsWith('/auth/providers')) {
            return reply({'googleEnabled': true});
          }
          if (request.url.path.endsWith('/auth/register')) {
            return reply({
              'user': {'id': 'u2', 'email': 'new@b.c', 'displayName': 'Newbie'},
            }, 201);
          }
          if (request.url.path.endsWith('/auth/login')) {
            return reply({
              'accessToken': 'at-signup',
              'user': {'id': 'u2', 'email': 'new@b.c', 'displayName': 'Newbie'},
            });
          }
          if (request.url.path.endsWith('/auth/me')) {
            return reply({
              'user': {'id': 'u2', 'email': 'new@b.c', 'displayName': 'Newbie'},
            });
          }
          if (request.url.path.endsWith('/broadcasts')) {
            return reply({'items': [], 'nextCursor': ''});
          }
          fail('unexpected ${request.url}');
        });
        await tester.pumpWidget(
          WbbApp(
            apiClient: ApiClient(client: client),
            initialUri: Uri.parse(
              'https://example.com/auth/google/complete?error=$errorCode',
            ),
          ),
        );
        await tester.pumpAndSettle();
        await tester.tap(find.text('로그인 다시 시도'));
        await tester.pumpAndSettle();
        await tester.tap(find.byKey(const Key('login-goto-signup')));
        await tester.pumpAndSettle();
        await tester.enterText(
          find.byKey(const Key('signup-email')),
          'new@b.c',
        );
        await tester.enterText(
          find.byKey(const Key('signup-password')),
          'correct-horse-battery',
        );
        await tester.enterText(
          find.byKey(const Key('signup-display-name')),
          'Newbie',
        );
        await tester.tap(find.byKey(const Key('signup-submit')));
        await tester.pumpAndSettle();
        // Signup success from the replaced-route path must reach the app — the
        // old double-pop stripped the only remaining route here.
        expect(find.byType(SignupScreen), findsNothing);
        expect(find.byType(LoginScreen), findsNothing);
        expect(find.text('등록한 방송이 없습니다. + 버튼으로 만드세요.'), findsOneWidget);
        expect(find.byKey(const Key('my-broadcasts-create')), findsOneWidget);
        await tester.pageBack();
        await tester.pumpAndSettle();
        await expectRootHomeSurvivesPlatformBack(tester);
      },
    );
  }

  testWidgets('home → login → email success returns to home', (tester) async {
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh')) {
        return reply({
          'error': {
            'code': 'unauthorized',
            'message': 'Authentication required.',
          },
        }, 401);
      }
      if (request.url.path.endsWith('/auth/providers')) {
        return reply({'googleEnabled': false});
      }
      if (request.url.path.endsWith('/auth/login')) {
        return reply({
          'accessToken': 'at1',
          'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
        });
      }
      if (request.url.path.endsWith('/broadcasts')) {
        return reply({'items': [], 'nextCursor': ''});
      }
      fail('unexpected ${request.url}');
    });
    await tester.pumpWidget(WbbApp(apiClient: ApiClient(client: client)));
    await tester.pumpAndSettle();
    await tester.tap(find.text('로그인'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('login-email')), 'a@b.c');
    await tester.enterText(
      find.byKey(const Key('login-password')),
      'correct-horse-battery',
    );
    await tester.tap(find.byKey(const Key('login-submit')));
    await tester.pumpAndSettle();
    expect(find.byType(LoginScreen), findsNothing);
    expect(find.text('Alice'), findsOneWidget);
    expect(find.text('아직 방송이 없습니다.'), findsOneWidget);
    await expectRootHomeSurvivesPlatformBack(tester);
  });

  testWidgets('home → login → signup success returns to home', (tester) async {
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh')) {
        return reply({
          'error': {
            'code': 'unauthorized',
            'message': 'Authentication required.',
          },
        }, 401);
      }
      if (request.url.path.endsWith('/auth/providers')) {
        return reply({'googleEnabled': false});
      }
      if (request.url.path.endsWith('/auth/register')) {
        return reply({
          'user': {'id': 'u2', 'email': 'new@b.c', 'displayName': 'Newbie'},
        }, 201);
      }
      if (request.url.path.endsWith('/auth/login')) {
        return reply({
          'accessToken': 'at2',
          'user': {'id': 'u2', 'email': 'new@b.c', 'displayName': 'Newbie'},
        });
      }
      if (request.url.path.endsWith('/broadcasts')) {
        return reply({'items': [], 'nextCursor': ''});
      }
      fail('unexpected ${request.url}');
    });
    await tester.pumpWidget(WbbApp(apiClient: ApiClient(client: client)));
    await tester.pumpAndSettle();
    await tester.tap(find.text('로그인'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('login-goto-signup')));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('signup-email')), 'new@b.c');
    await tester.enterText(
      find.byKey(const Key('signup-password')),
      'correct-horse-battery',
    );
    await tester.enterText(
      find.byKey(const Key('signup-display-name')),
      'Newbie',
    );
    await tester.tap(find.byKey(const Key('signup-submit')));
    await tester.pumpAndSettle();
    // Default double-pop path: Signup+Login pop back to the (working) home.
    expect(find.byType(SignupScreen), findsNothing);
    expect(find.byType(LoginScreen), findsNothing);
    expect(find.text('Newbie'), findsOneWidget);
    expect(find.text('아직 방송이 없습니다.'), findsOneWidget);
    await expectRootHomeSurvivesPlatformBack(tester);
  });

  testWidgets(
    'account conflict explains that the existing login should be used',
    (tester) async {
      final client = MockClient((request) async {
        if (request.url.path.endsWith('/auth/refresh')) {
          return reply({
            'error': {
              'code': 'unauthorized',
              'message': 'Authentication required.',
            },
          }, 401);
        }
        fail('unexpected ${request.url}');
      });
      await tester.pumpWidget(
        WbbApp(
          apiClient: ApiClient(client: client),
          initialUri: Uri.parse(
            'https://example.com/auth/google/complete?error=account_exists',
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('기존 방법으로 로그인'), findsOneWidget);
      expect(find.text('내 방송'), findsNothing);
    },
  );
}
