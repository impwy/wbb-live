import 'dart:convert';
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';
import 'package:wbb_frontend/api/wbb_api.dart';
import 'package:wbb_frontend/media/live_publisher.dart';
import 'package:wbb_frontend/media/media_platform.dart';
import 'package:wbb_frontend/screens/broadcast_detail_screen.dart';
import 'package:wbb_frontend/state/auth_controller.dart';

http.Response jsonResponse(Object body, [int status = 200]) => http.Response(
  jsonEncode(body),
  status,
  headers: {'content-type': 'application/json'},
);

class SessionUiApiClient extends ApiClient {
  SessionUiApiClient({required super.client});
  @override
  Future<WhipResource> postWhip(
    PreparedSession prepared,
    String offerSdp,
  ) async => WhipResource(
    prepared.session.id,
    '/api/v1/live-sessions/${prepared.session.id}/whip/test-resource',
    null,
    'v=0',
  );
  @override
  Future<void> deleteWhip(WhipResource resource) async {}
}

class SessionUiMedia implements MediaPlatform {
  @override
  PublisherMediaAttempt newPublisher(
    PublishSource source,
    void Function() onCaptureEnded,
  ) => SessionUiAttempt();
  @override
  ViewerMediaPlayer newViewer(String url, void Function(String) onEvent) =>
      throw UnimplementedError();
}

class SessionUiAttempt implements PublisherMediaAttempt {
  @override
  Widget get preview => const SizedBox();
  @override
  Future<CaptureOffer> captureAndOffer() async =>
      const CaptureOffer(sdp: 'v=0', sharedAudio: true, audioRunning: true);
  @override
  Future<void> acceptAnswer(String answerSdp) async {}
  @override
  Future<bool> resumeAudio() async => true;
  @override
  Future<void> stopLocal() async {}
}

Map<String, Object?> get broadcast => {
  'id': 'b1',
  'ownerId': 'u1',
  'owner': {'id': 'u1', 'displayName': 'Alice'},
  'title': '로컬 방송',
  'description': '',
  'version': 1,
  'status': 'ready',
  'currentSessionId': null,
  'createdAt': '2026-11-09T01:00:00Z',
  'updatedAt': '2026-11-09T01:00:00Z',
};

Map<String, Object?> get session => {
  'id': 's1',
  'broadcastId': 'b1',
  'state': 'starting',
  'version': 1,
  'startedAt': '2026-11-09T01:00:00Z',
  'endedAt': null,
};

void main() {
  for (final cancel in [false, true]) {
    for (final leavePending in [false, true]) {
      testWidgets(
        'slow poll ${leavePending ? 'never returns' : 'returns late'} after ${cancel ? 'cancel' : 'prepare'}',
        (tester) async {
          var current = <String, Object?>{'session': cancel ? session : null};
          Completer<http.Response>? delayed;
          Completer<void>? requested;
          final api = SessionUiApiClient(
            client: MockClient((request) async {
              final path = request.url.path;
              if (path.endsWith('/auth/login')) {
                return jsonResponse({
                  'accessToken': 'owner-access',
                  'user': {
                    'id': 'u1',
                    'email': 'a@b.c',
                    'displayName': 'Alice',
                  },
                });
              }
              if (path.endsWith('/broadcasts/b1') && request.method == 'GET') {
                return jsonResponse({
                  'broadcast': {
                    ...broadcast,
                    'status': cancel ? 'starting' : 'ready',
                  },
                });
              }
              if (path.endsWith('/broadcasts/b1/sessions/current')) {
                final pending = delayed;
                if (pending != null) {
                  delayed = null;
                  requested!.complete();
                  return pending.future;
                }
                return jsonResponse(current);
              }
              if (path.endsWith('/broadcasts/b1/sessions') &&
                  request.method == 'POST') {
                current = {'session': session};
                return jsonResponse({
                  'session': session,
                  'publishGrant': null,
                }, 202);
              }
              if (path.endsWith('/broadcasts/b1/sessions/s1/stop')) {
                current = {
                  'session': {...session, 'state': 'stopping', 'version': 2},
                };
                return jsonResponse(current, 202);
              }
              fail('unexpected ${request.method} $path');
            }),
          );
          final auth = AuthController(api);
          await auth.login('a@b.c', 'pw');
          await tester.pumpWidget(
            MultiProvider(
              providers: [
                Provider<ApiClient>.value(value: api),
                ChangeNotifierProvider(
                  create: (_) => LivePublisher(api, SessionUiMedia()),
                ),
                ChangeNotifierProvider<AuthController>.value(value: auth),
              ],
              child: const MaterialApp(
                home: BroadcastDetailScreen(broadcastId: 'b1'),
              ),
            ),
          );
          await tester.pumpAndSettle();
          final stale = Completer<http.Response>();
          delayed = stale;
          requested = Completer<void>();
          await tester.pump(const Duration(seconds: 5));
          await tester.pump();
          await requested.future;
          final actionKey = Key(cancel ? 'session-cancel' : 'session-prepare');
          await tester.tap(find.byKey(actionKey));
          await tester.pumpAndSettle();
          if (leavePending) {
            current = {
              'session': {...session, 'state': 'ended', 'version': 3},
            };
            await tester.pump(const Duration(seconds: 5));
            await tester.pumpAndSettle();
            expect(find.text('상태: 종료'), findsOneWidget);
            expect(find.byKey(const Key('session-prepare')), findsOneWidget);
            // The unresolved request proved it could not block the current
            // poll. Release the fake transport before the test clock ends.
            stale.complete(jsonResponse({'session': cancel ? session : null}));
            await tester.pump();
          } else {
            stale.complete(jsonResponse({'session': cancel ? session : null}));
            await tester.pumpAndSettle();
            if (cancel) {
              expect(find.textContaining('종료 처리 중'), findsWidgets);
              expect(find.byKey(const Key('session-cancel')), findsNothing);
            } else {
              expect(find.textContaining('준비 중'), findsWidgets);
              expect(find.byKey(const Key('session-cancel')), findsOneWidget);
            }
          }
        },
      );
    }
  }
  testWidgets('old completion does not unlock a newer pending poll', (
    tester,
  ) async {
    final old = Completer<http.Response>();
    final newest = Completer<http.Response>();
    var currentCalls = 0;
    final api = SessionUiApiClient(
      client: MockClient((request) async {
        final path = request.url.path;
        if (path.endsWith('/auth/login')) {
          return jsonResponse({
            'accessToken': 'owner-access',
            'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
          });
        }
        if (path.endsWith('/broadcasts/b1') && request.method == 'GET') {
          return jsonResponse({'broadcast': broadcast});
        }
        if (path.endsWith('/broadcasts/b1/sessions/current')) {
          currentCalls++;
          if (currentCalls == 1) return jsonResponse({'session': null});
          if (currentCalls == 2) return old.future;
          if (currentCalls == 3) return newest.future;
          return jsonResponse({'session': session});
        }
        if (path.endsWith('/broadcasts/b1/sessions') &&
            request.method == 'POST') {
          return jsonResponse({'session': session, 'publishGrant': null}, 202);
        }
        fail('unexpected ${request.method} $path');
      }),
    );
    final auth = AuthController(api);
    await auth.login('a@b.c', 'pw');
    await tester.pumpWidget(
      MultiProvider(
        providers: [
          Provider<ApiClient>.value(value: api),
          ChangeNotifierProvider(
            create: (_) => LivePublisher(api, SessionUiMedia()),
          ),
          ChangeNotifierProvider<AuthController>.value(value: auth),
        ],
        child: const MaterialApp(
          home: BroadcastDetailScreen(broadcastId: 'b1'),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.pump(const Duration(seconds: 5));
    await tester.pump();
    expect(currentCalls, 2);
    await tester.tap(find.byKey(const Key('session-prepare')));
    await tester.pump();
    expect(currentCalls, 3);
    old.complete(jsonResponse({'session': null}));
    await tester.pump();
    await tester.pump(const Duration(seconds: 5));
    await tester.pump();
    expect(currentCalls, 3);
    newest.complete(jsonResponse({'session': session}));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('session-cancel')), findsOneWidget);
  });
  testWidgets(
    'signed-out visitor reads preparation status without control or playback',
    (tester) async {
      final api = SessionUiApiClient(
        client: MockClient((request) async {
          if (request.method == 'GET' &&
              request.url.path.endsWith('/broadcasts/b1')) {
            return jsonResponse({
              'broadcast': {
                ...broadcast,
                'status': 'starting',
                'currentSessionId': 's1',
              },
            });
          }
          if (request.method == 'GET' &&
              request.url.path.endsWith('/broadcasts/b1/sessions/current')) {
            return jsonResponse({'session': session});
          }
          fail(
            'visitor made protected call: ${request.method} ${request.url.path}',
          );
        }),
      );
      await tester.pumpWidget(
        MultiProvider(
          providers: [
            Provider<ApiClient>.value(value: api),
            ChangeNotifierProvider(
              create: (_) => LivePublisher(api, SessionUiMedia()),
            ),
            ChangeNotifierProvider<AuthController>(
              create: (_) => AuthController(api),
            ),
          ],
          child: const MaterialApp(
            home: BroadcastDetailScreen(broadcastId: 'b1'),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('session-state')), findsOneWidget);
      expect(find.textContaining('준비 중'), findsWidgets);
      expect(find.byKey(const Key('session-prepare')), findsNothing);
      expect(find.byKey(const Key('session-cancel')), findsNothing);
      expect(
        tester
            .widget<FilledButton>(find.byKey(const Key('playback-play')))
            .onPressed,
        isNull,
      );
      await tester.tap(find.byKey(const Key('detail-refresh')));
      await tester.pumpAndSettle();
      expect(find.textContaining('준비 중'), findsWidgets);
    },
  );

  testWidgets(
    'owner prepares and cancels while visitor sees only public state',
    (tester) async {
      var current = <String, Object?>{'session': null};
      var startCalls = 0;
      var stopCalls = 0;
      final api = SessionUiApiClient(
        client: MockClient((request) async {
          final p = request.url.path;
          if (p.endsWith('/auth/login')) {
            return jsonResponse({
              'accessToken': 'owner-access',
              'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
            });
          }
          if (p.endsWith('/broadcasts/b1') && request.method == 'GET') {
            return jsonResponse({'broadcast': broadcast});
          }
          if (p.endsWith('/broadcasts/b1/sessions/current')) {
            return jsonResponse(current);
          }
          if (p.endsWith('/broadcasts/b1/sessions') &&
              request.method == 'POST') {
            startCalls++;
            expect(request.headers['Idempotency-Key'], isNotEmpty);
            expect(jsonDecode(request.body)['source'], 'screen');
            current = {'session': session};
            return jsonResponse({
              'session': session,
              'publishGrant': {
                'token': 'private',
                'expiresAt': '2026-11-09T01:00:30Z',
                'whipUrl': '/api/v1/live-sessions/s1/whip',
              },
            }, 202);
          }
          if (p.endsWith('/broadcasts/b1/sessions/s1/stop')) {
            stopCalls++;
            current = {
              'session': {...session, 'state': 'stopping', 'version': 2},
            };
            return jsonResponse(current, 202);
          }
          fail('unexpected ${request.method} $p');
        }),
      );
      final auth = AuthController(api);
      await auth.login('a@b.c', 'pw');
      await tester.pumpWidget(
        MultiProvider(
          providers: [
            Provider<ApiClient>.value(value: api),
            ChangeNotifierProvider(
              create: (_) => LivePublisher(api, SessionUiMedia()),
            ),
            ChangeNotifierProvider<AuthController>.value(value: auth),
          ],
          child: const MaterialApp(
            home: BroadcastDetailScreen(broadcastId: 'b1'),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('session-prepare')), findsOneWidget);
      expect(find.textContaining('송출 중'), findsNothing);
      await tester.tap(find.byKey(const Key('session-prepare')));
      await tester.pumpAndSettle();
      expect(startCalls, 1);
      expect(find.textContaining('준비 중'), findsWidgets);
      expect(find.byKey(const Key('session-cancel')), findsOneWidget);
      expect(find.text('private'), findsNothing);
      await tester.tap(find.byKey(const Key('session-cancel')));
      await tester.pumpAndSettle();
      expect(stopCalls, 1);
      expect(find.textContaining('종료 처리 중'), findsWidgets);
      expect(find.byKey(const Key('session-prepare')), findsNothing);
    },
  );
}
