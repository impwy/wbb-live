import 'dart:async';
import 'dart:convert';

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

class ClickAttempt implements PublisherMediaAttempt {
  ClickAttempt({this.initialAudioRunning = true, this.sharedAudio = true});
  final bool initialAudioRunning;
  final bool sharedAudio;
  int captures = 0;
  int stops = 0;
  int resumes = 0;
  @override
  Widget get preview => const SizedBox();
  @override
  Future<CaptureOffer> captureAndOffer() {
    captures++;
    return Future.value(
      CaptureOffer(
        sdp: 'v=0',
        sharedAudio: sharedAudio,
        audioRunning: initialAudioRunning,
      ),
    );
  }

  @override
  Future<void> acceptAnswer(String answerSdp) async {}
  @override
  Future<bool> resumeAudio() async {
    resumes++;
    return true;
  }

  @override
  Future<void> stopLocal() async {
    stops++;
  }
}

class ViewingPlayer implements ViewerMediaPlayer {
  bool disposed = false;
  @override
  Widget get view => const SizedBox(key: Key('fake-video'));
  @override
  Future<void> play({required bool withSound}) async {}
  @override
  void goLive() {}
  @override
  void dispose() {
    disposed = true;
  }
}

class ViewingMedia implements MediaPlatform {
  final players = <ViewingPlayer>[];
  @override
  PublisherMediaAttempt newPublisher(
    PublishSource source,
    void Function() onCaptureEnded,
  ) => ClickAttempt();
  @override
  ViewerMediaPlayer newViewer(String url, void Function(String) onEvent) {
    final player = ViewingPlayer();
    players.add(player);
    return player;
  }
}

Map<String, Object?> broadcastJson({String status = 'ready'}) => {
  'id': 'b1',
  'ownerId': 'u1',
  'owner': {'id': 'u1', 'displayName': 'A'},
  'title': '내 방송',
  'description': '',
  'version': 1,
  'status': status,
  'createdAt': '2026-11-09T01:00:00Z',
  'updatedAt': '2026-11-09T01:00:00Z',
};

Map<String, Object?> sessionJson(String id, String state) => {
  'id': id,
  'broadcastId': 'b1',
  'state': state,
  'version': 1,
  'startedAt': '2026-11-09T01:00:00Z',
};

Future<void> pumpDetail(
  WidgetTester tester,
  ApiClient api,
  AuthController auth,
  LivePublisher publisher,
  MediaPlatform media,
) async {
  await tester.pumpWidget(
    MultiProvider(
      providers: [
        Provider<ApiClient>.value(value: api),
        ChangeNotifierProvider<AuthController>.value(value: auth),
        ChangeNotifierProvider<LivePublisher>.value(value: publisher),
        Provider<MediaPlatform>.value(value: media),
      ],
      child: const MaterialApp(home: BroadcastDetailScreen(broadcastId: 'b1')),
    ),
  );
  await tester.pumpAndSettle();
}

class ClickMedia implements MediaPlatform {
  ClickMedia(this.attempt);
  final ClickAttempt attempt;
  final sources = <PublishSource>[];
  @override
  PublisherMediaAttempt newPublisher(
    PublishSource source,
    void Function() onCaptureEnded,
  ) {
    sources.add(source);
    return attempt;
  }

  @override
  ViewerMediaPlayer newViewer(String url, void Function(String) onEvent) =>
      throw UnimplementedError();
}

class PendingAttempt implements PublisherMediaAttempt {
  final capture = Completer<CaptureOffer>();
  int stops = 0;
  @override
  Widget get preview => const SizedBox();
  @override
  Future<CaptureOffer> captureAndOffer() => capture.future;
  @override
  Future<void> acceptAnswer(String answerSdp) async {}
  @override
  Future<bool> resumeAudio() async => true;
  @override
  Future<void> stopLocal() async {
    stops++;
  }
}

class PendingMedia implements MediaPlatform {
  PendingMedia(this.attempt);
  final PendingAttempt attempt;
  @override
  PublisherMediaAttempt newPublisher(
    PublishSource source,
    void Function() onCaptureEnded,
  ) => attempt;
  @override
  ViewerMediaPlayer newViewer(String url, void Function(String) onEvent) =>
      throw UnimplementedError();
}

class QueuePendingMedia implements MediaPlatform {
  QueuePendingMedia(this.attempts);
  final List<PendingAttempt> attempts;
  int created = 0;
  @override
  PublisherMediaAttempt newPublisher(
    PublishSource source,
    void Function() onCaptureEnded,
  ) {
    created++;
    return attempts.removeAt(0);
  }

  @override
  ViewerMediaPlayer newViewer(String url, void Function(String) onEvent) =>
      throw UnimplementedError();
}

http.Response response(Object body, [int status = 200]) => http.Response(
  jsonEncode(body),
  status,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

void main() {
  for (final lifecycle in [true, false]) {
    testWidgets(
      'pending camera permission stop ${lifecycle ? 'hidden/resumed' : 'JS-first'} allows a new attempt',
      (tester) async {
        final first = PendingAttempt(), second = PendingAttempt();
        final media = QueuePendingMedia([first, second]);
        var prepareCalls = 0;
        final api = ApiClient(
          baseUrl: 'https://live.test/api/v1',
          client: MockClient((request) async {
            final path = request.url.path;
            if (path.endsWith('/auth/login')) {
              return response({
                'accessToken': 'access',
                'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'A'},
              });
            }
            if (path.endsWith('/broadcasts/b1') && request.method == 'GET') {
              return response({'broadcast': broadcastJson()});
            }
            if (path.endsWith('/sessions/current')) {
              return response({'session': null});
            }
            if (path.endsWith('/sessions') && request.method == 'POST') {
              prepareCalls++;
            }
            fail('unexpected ${request.method} $path');
          }),
        );
        final auth = AuthController(api);
        await auth.login('a@b.c', 'pw');
        final publisher = LivePublisher(api, media);
        await pumpDetail(tester, api, auth, publisher, media);
        await tester.tap(find.byKey(const Key('session-prepare-camera')));
        await tester.pump();
        expect(publisher.phase, PublishPhase.capturing);
        if (lifecycle) {
          tester.binding.handleAppLifecycleStateChanged(
            AppLifecycleState.inactive,
          );
          await tester.pump();
          expect(first.stops, 0);
          tester.binding.handleAppLifecycleStateChanged(
            AppLifecycleState.hidden,
          );
          await tester.pump();
          // Real browsers pass through inactive on the way back.
          tester.binding.handleAppLifecycleStateChanged(
            AppLifecycleState.inactive,
          );
          tester.binding.handleAppLifecycleStateChanged(
            AppLifecycleState.resumed,
          );
        } else {
          await publisher.stop(); // same callback path as browser pagehide
        }
        await tester.pumpAndSettle();
        expect(first.stops, greaterThan(0));
        final restart = tester.widget<FilledButton>(
          find.byKey(const Key('session-prepare-camera')),
        );
        expect(restart.onPressed, isNotNull);
        await tester.tap(find.byKey(const Key('session-prepare-camera')));
        await tester.pump();
        expect(media.created, 2);
        expect(publisher.phase, PublishPhase.capturing);
        first.capture.complete(
          const CaptureOffer(sdp: 'v=0', sharedAudio: true, audioRunning: true),
        );
        await tester.pump();
        expect(prepareCalls, 0);
        expect(publisher.phase, PublishPhase.capturing);
        await publisher.stop();
        second.capture.completeError(Exception('cancelled'));
        await tester.pump();
      },
    );
  }

  for (final selected in PublishSource.values) {
    testWidgets(
      '${selected.name} button prepares matching source and hides safely',
      (tester) async {
        final attempt = ClickAttempt(sharedAudio: false);
        final media = ClickMedia(attempt);
        var prepared = false, stopping = false, stops = 0, detailReads = 0;
        String? preparedSource;
        final api = ApiClient(
          baseUrl: 'https://live.test/api/v1',
          client: MockClient((request) async {
            final path = request.url.path;
            if (path.endsWith('/auth/login')) {
              return response({
                'accessToken': 'access',
                'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'A'},
              });
            }
            if (path.endsWith('/broadcasts/b1') && request.method == 'GET') {
              detailReads++;
              return response({
                'broadcast': broadcastJson(
                  status: stopping
                      ? 'stopping'
                      : prepared
                      ? 'starting'
                      : 'ready',
                ),
              });
            }
            if (path.endsWith('/sessions/current')) {
              return response({
                'session': prepared
                    ? sessionJson('s1', stopping ? 'stopping' : 'starting')
                    : null,
              });
            }
            if (path.endsWith('/broadcasts/b1/sessions') &&
                request.method == 'POST') {
              prepared = true;
              preparedSource =
                  (jsonDecode(request.body) as Map<String, dynamic>)['source']
                      as String;
              return response({
                'session': sessionJson('s1', 'starting'),
                'publishGrant': {
                  'token': 'grant',
                  'whipUrl': '/api/v1/live-sessions/s1/whip',
                  'expiresAt': '2026-11-09T01:00:30Z',
                },
              }, 202);
            }
            if (path.endsWith('/whip') && request.method == 'POST') {
              return http.Response(
                'v=0\r\nanswer',
                201,
                headers: {
                  'content-type': 'application/sdp',
                  'location': '/api/v1/live-sessions/s1/whip/r1',
                },
              );
            }
            if (path.endsWith('/whip/r1') && request.method == 'DELETE') {
              return http.Response('', 204);
            }
            if (path.endsWith('/sessions/s1/stop') &&
                request.method == 'POST') {
              stops++;
              stopping = true;
              return response({'session': sessionJson('s1', 'stopping')}, 202);
            }
            fail('unexpected ${request.method} $path');
          }),
        );
        final auth = AuthController(api);
        await auth.login('a@b.c', 'pw');
        final publisher = LivePublisher(api, media);
        await pumpDetail(tester, api, auth, publisher, media);
        await tester.tap(
          find.byKey(
            Key(
              selected == PublishSource.camera
                  ? 'session-prepare-camera'
                  : 'session-prepare',
            ),
          ),
        );
        await tester.pumpAndSettle();
        expect(media.sources, [selected]);
        expect(preparedSource, selected.name);
        expect(publisher.phase, PublishPhase.publishing);
        expect(
          find.text('공유 소리가 없습니다. 소리를 포함한 탭을 다시 선택해 주세요.'),
          selected == PublishSource.camera ? findsNothing : findsOneWidget,
        );
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.inactive,
        );
        await tester.pump();
        expect(attempt.stops, 0);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        await tester.pumpAndSettle();
        expect(attempt.stops, selected == PublishSource.camera ? 1 : 0);
        expect(stops, selected == PublishSource.camera ? 1 : 0);
        final beforeResume = detailReads;
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.inactive,
        );
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.resumed,
        );
        await tester.pumpAndSettle();
        expect(detailReads, greaterThan(beforeResume));
        expect(media.sources, [selected]); // no automatic restart
        if (selected == PublishSource.screen) {
          await tester.tap(find.byKey(const Key('session-cancel')));
          await tester.pumpAndSettle();
          expect(attempt.stops, 1);
        }
      },
    );
  }

  testWidgets(
    'leaving owner detail stops active capture before permission resolves',
    (tester) async {
      final attempt = PendingAttempt();
      final media = PendingMedia(attempt);
      var prepareCalls = 0;
      final api = ApiClient(
        baseUrl: 'https://live.test/api/v1',
        client: MockClient((request) async {
          final path = request.url.path;
          if (path.endsWith('/auth/login')) {
            return response({
              'accessToken': 'access',
              'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'A'},
            });
          }
          if (path.endsWith('/broadcasts/b1') && request.method == 'GET') {
            return response({'broadcast': broadcastJson()});
          }
          if (path.endsWith('/sessions/current')) {
            return response({'session': null});
          }
          if (path.endsWith('/sessions') && request.method == 'POST') {
            prepareCalls++;
          }
          fail('unexpected ${request.method} $path');
        }),
      );
      final auth = AuthController(api);
      await auth.login('a@b.c', 'pw');
      final publisher = LivePublisher(api, media);
      await pumpDetail(tester, api, auth, publisher, media);
      await tester.tap(find.byKey(const Key('session-prepare')));
      await tester.pump();
      expect(publisher.phase, PublishPhase.capturing);
      await tester.pumpWidget(const SizedBox());
      expect(attempt.stops, 1);
      attempt.capture.completeError(Exception('permission cancelled'));
      await tester.pump();
      expect(prepareCalls, 0);
    },
  );

  testWidgets(
    'owner can cancel while prepare response is pending; late session is stopped',
    (tester) async {
      final prepared = Completer<http.Response>();
      final entered = Completer<void>();
      final attempt = ClickAttempt();
      var stopCalls = 0;
      var whipCalls = 0;
      var current = <String, Object?>{'session': null};
      final api = ApiClient(
        client: MockClient((request) async {
          final p = request.url.path;
          if (p.endsWith('/auth/login')) {
            return response({
              'accessToken': 'access',
              'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'A'},
            });
          }
          if (p.endsWith('/broadcasts/b1') && request.method == 'GET') {
            return response({
              'broadcast': {
                'id': 'b1',
                'ownerId': 'u1',
                'owner': {'id': 'u1', 'displayName': 'A'},
                'title': '내 방송',
                'description': '',
                'version': 1,
                'status': 'ready',
                'createdAt': '2026-11-09T01:00:00Z',
                'updatedAt': '2026-11-09T01:00:00Z',
              },
            });
          }
          if (p.endsWith('/broadcasts/b1/sessions/current')) {
            return response(current);
          }
          if (p.endsWith('/broadcasts/b1/sessions') &&
              request.method == 'POST') {
            entered.complete();
            return prepared.future;
          }
          if (p.endsWith('/broadcasts/b1/sessions/s1/stop')) {
            stopCalls++;
            current = {
              'session': {
                'id': 's1',
                'broadcastId': 'b1',
                'state': 'stopping',
                'version': 2,
                'startedAt': '2026-11-09T01:00:00Z',
              },
            };
            return response(current, 202);
          }
          if (p.contains('/whip')) whipCalls++;
          fail('unexpected ${request.method} $p');
        }),
      );
      final auth = AuthController(api);
      await auth.login('a@b.c', 'pw');
      final publisher = LivePublisher(api, ClickMedia(attempt));
      await tester.pumpWidget(
        MultiProvider(
          providers: [
            Provider<ApiClient>.value(value: api),
            ChangeNotifierProvider<AuthController>.value(value: auth),
            ChangeNotifierProvider<LivePublisher>.value(value: publisher),
          ],
          child: const MaterialApp(
            home: BroadcastDetailScreen(broadcastId: 'b1'),
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('session-prepare')));
      expect(attempt.captures, 1);
      await entered.future;
      await tester.pump();
      final cancel = tester.widget<OutlinedButton>(
        find.byKey(const Key('session-cancel')),
      );
      expect(cancel.onPressed, isNotNull);
      await tester.tap(find.byKey(const Key('session-cancel')));
      await tester.pump();
      prepared.complete(
        response({
          'session': {
            'id': 's1',
            'broadcastId': 'b1',
            'state': 'starting',
            'version': 1,
            'startedAt': '2026-11-09T01:00:00Z',
          },
          'publishGrant': null,
        }, 202),
      );
      await tester.pumpAndSettle();
      expect(stopCalls, 1);
      expect(whipCalls, 0);
      expect(attempt.stops, 1);
    },
  );

  testWidgets('failed server stop remains visible through publisher provider', (
    tester,
  ) async {
    final attempt = ClickAttempt();
    final media = ClickMedia(attempt);
    var prepared = false;
    final requests = <String>[];
    final api = ApiClient(
      baseUrl: 'https://live.test/api/v1',
      client: MockClient((request) async {
        final path = request.url.path;
        requests.add('${request.method} $path');
        if (path.endsWith('/auth/login')) {
          return response({
            'accessToken': 'access',
            'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'A'},
          });
        }
        if (path.endsWith('/broadcasts/b1') && request.method == 'GET') {
          return response({
            'broadcast': broadcastJson(status: prepared ? 'starting' : 'ready'),
          });
        }
        if (path.endsWith('/sessions/current')) {
          return response({
            'session': prepared ? sessionJson('s1', 'starting') : null,
          });
        }
        if (path.endsWith('/broadcasts/b1/sessions') &&
            request.method == 'POST') {
          prepared = true;
          return response({
            'session': sessionJson('s1', 'starting'),
            'publishGrant': {
              'token': 'private',
              'whipUrl': '/api/v1/live-sessions/s1/whip',
              'expiresAt': '2026-11-09T01:00:30Z',
            },
          }, 202);
        }
        if (path.endsWith('/whip') && request.method == 'POST') {
          return http.Response(
            'v=0\r\nanswer',
            201,
            headers: {
              'content-type': 'application/sdp',
              'location': '/api/v1/live-sessions/s1/whip/r1',
            },
          );
        }
        if (request.method == 'DELETE' || path.endsWith('/sessions/s1/stop')) {
          return response({
            'error': {'code': 'unavailable', 'message': 'unavailable'},
          }, 500);
        }
        fail('unexpected ${request.method} $path');
      }),
    );
    final auth = AuthController(api);
    await auth.login('a@b.c', 'pw');
    final publisher = LivePublisher(api, media);
    await pumpDetail(tester, api, auth, publisher, media);
    await tester.tap(find.byKey(const Key('session-prepare')));
    await tester.pumpAndSettle();
    // Diagnostic in failure output if the action did not reach publishing.
    expect(
      publisher.phase,
      PublishPhase.publishing,
      reason: '${publisher.message} $requests',
    );
    expect(find.text('송출: 송출 중'), findsOneWidget);
    await tester.tap(find.byKey(const Key('session-cancel')));
    await tester.pumpAndSettle();
    expect(attempt.stops, 1);
    expect(find.text('서버 종료를 확인하지 못했습니다. 상태를 새로고침해 주세요.'), findsOneWidget);
    expect(find.text('송출: 오류'), findsOneWidget);
  });

  testWidgets(
    'new live session disposes old player before delayed playback reply',
    (tester) async {
      final media = ViewingMedia();
      final delayed = Completer<http.Response>();
      var currentId = 's1';
      final api = ApiClient(
        baseUrl: 'https://live.test/api/v1',
        client: MockClient((request) async {
          final path = request.url.path;
          if (path.endsWith('/broadcasts/b1') && request.method == 'GET') {
            return response({'broadcast': broadcastJson(status: 'live')});
          }
          if (path.endsWith('/sessions/current')) {
            return response({'session': sessionJson(currentId, 'live')});
          }
          if (path.endsWith('/playback')) {
            if (currentId == 's2') return delayed.future;
            return response({
              'sessionId': 's1',
              'masterUrl': '/media/wbb/s1/1/index.m3u8',
            });
          }
          fail('unexpected ${request.method} $path');
        }),
      );
      final auth = AuthController(api);
      final publisher = LivePublisher(api, media);
      await pumpDetail(tester, api, auth, publisher, media);
      await tester.pumpAndSettle();
      expect(media.players, hasLength(1));
      final old = media.players.single;
      expect(
        tester
            .widget<FilledButton>(find.byKey(const Key('playback-play')))
            .onPressed,
        isNotNull,
      );
      currentId = 's2';
      await tester.tap(find.byKey(const Key('detail-refresh')));
      await tester.pump();
      await tester.pump();
      expect(old.disposed, isTrue);
      expect(find.byKey(const Key('fake-video')), findsNothing);
      expect(
        tester
            .widget<FilledButton>(find.byKey(const Key('playback-play')))
            .onPressed,
        isNull,
      );
      delayed.complete(
        response({
          'error': {'code': 'not_ready', 'message': 'wait'},
        }, 409),
      );
      await tester.pumpAndSettle();
      expect(media.players, hasLength(1));
      expect(
        tester
            .widget<FilledButton>(find.byKey(const Key('playback-play')))
            .onPressed,
        isNull,
      );
    },
  );

  testWidgets(
    'suspended audio resumes from user button and provider updates screen',
    (tester) async {
      final attempt = ClickAttempt(initialAudioRunning: false);
      final media = ClickMedia(attempt);
      var prepared = false;
      final api = ApiClient(
        baseUrl: 'https://live.test/api/v1',
        client: MockClient((request) async {
          final path = request.url.path;
          if (path.endsWith('/auth/login')) {
            return response({
              'accessToken': 'access',
              'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'A'},
            });
          }
          if (path.endsWith('/broadcasts/b1') && request.method == 'GET') {
            return response({
              'broadcast': broadcastJson(
                status: prepared ? 'starting' : 'ready',
              ),
            });
          }
          if (path.endsWith('/sessions/current')) {
            return response({
              'session': prepared ? sessionJson('s1', 'starting') : null,
            });
          }
          if (path.endsWith('/broadcasts/b1/sessions') &&
              request.method == 'POST') {
            prepared = true;
            return response({
              'session': sessionJson('s1', 'starting'),
              'publishGrant': {
                'token': 'private',
                'whipUrl': '/api/v1/live-sessions/s1/whip',
                'expiresAt': '2026-11-09T01:00:30Z',
              },
            }, 202);
          }
          if (path.endsWith('/whip') && request.method == 'POST') {
            return http.Response(
              'v=0\r\nanswer',
              201,
              headers: {
                'content-type': 'application/sdp',
                'location': '/api/v1/live-sessions/s1/whip/r1',
              },
            );
          }
          if (request.method == 'DELETE') return http.Response('', 204);
          if (path.endsWith('/sessions/s1/stop')) {
            return response({'session': sessionJson('s1', 'stopping')}, 202);
          }
          fail('unexpected ${request.method} $path');
        }),
      );
      final auth = AuthController(api);
      await auth.login('a@b.c', 'pw');
      final publisher = LivePublisher(api, media);
      await pumpDetail(tester, api, auth, publisher, media);
      await tester.tap(find.byKey(const Key('session-prepare')));
      await tester.pumpAndSettle();
      expect(
        publisher.phase,
        PublishPhase.publishing,
        reason: publisher.message,
      );
      expect(find.byKey(const Key('publish-resume-audio')), findsOneWidget);
      await tester.tap(find.byKey(const Key('publish-resume-audio')));
      await tester.pumpAndSettle();
      expect(attempt.resumes, 1);
      expect(find.byKey(const Key('publish-resume-audio')), findsNothing);
      expect(find.text('오디오 실행 중'), findsOneWidget);
    },
  );
}
