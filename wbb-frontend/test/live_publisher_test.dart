import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:wbb_frontend/api/wbb_api.dart';
import 'package:wbb_frontend/media/live_publisher.dart';
import 'package:wbb_frontend/media/media_platform.dart';
import 'package:wbb_frontend/state/auth_controller.dart';

class FakeAttempt implements PublisherMediaAttempt {
  FakeAttempt(this.capture);
  final Completer<CaptureOffer> capture;
  int captureCalls = 0;
  int stopCalls = 0;
  @override
  Widget get preview => const SizedBox();
  @override
  Future<CaptureOffer> captureAndOffer() {
    captureCalls++;
    return capture.future;
  }

  @override
  Future<void> acceptAnswer(String answerSdp) async {}
  @override
  Future<bool> resumeAudio() async => true;
  @override
  Future<void> stopLocal() async {
    stopCalls++;
  }
}

class FakePlatform implements MediaPlatform {
  FakePlatform(this.attempt);
  final FakeAttempt attempt;
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

class QueuePlatform implements MediaPlatform {
  QueuePlatform(this.attempts);
  final List<FakeAttempt> attempts;
  @override
  PublisherMediaAttempt newPublisher(
    PublishSource source,
    void Function() onCaptureEnded,
  ) => attempts.removeAt(0);
  @override
  ViewerMediaPlayer newViewer(String url, void Function(String) onEvent) =>
      throw UnimplementedError();
}

void main() {
  test(
    'camera WHIP rejection stops its local tracks and prepared session',
    () async {
      final capture = Completer<CaptureOffer>()
        ..complete(
          const CaptureOffer(sdp: 'v=0', sharedAudio: true, audioRunning: true),
        );
      final attempt = FakeAttempt(capture);
      var whipCalls = 0, stopCalls = 0;
      final api = ApiClient(
        baseUrl: 'https://live.test/api/v1',
        client: MockClient((request) async {
          final path = request.url.path;
          if (path.endsWith('/broadcasts/b1/sessions') &&
              request.method == 'POST') {
            return http.Response(
              jsonEncode({
                'session': {
                  'id': 's1',
                  'broadcastId': 'b1',
                  'state': 'starting',
                  'version': 1,
                  'startedAt': '2026-11-09T01:00:00Z',
                },
                'publishGrant': {
                  'token': 'private',
                  'whipUrl': '/api/v1/live-sessions/s1/whip',
                  'expiresAt': '2026-11-09T01:00:30Z',
                },
              }),
              202,
              headers: {'content-type': 'application/json'},
            );
          }
          if (path.endsWith('/live-sessions/s1/whip') &&
              request.method == 'POST') {
            whipCalls++;
            return http.Response('', 503);
          }
          if (path.endsWith('/broadcasts/b1/sessions/s1/stop') &&
              request.method == 'POST') {
            stopCalls++;
            return http.Response(
              jsonEncode({
                'session': {
                  'id': 's1',
                  'broadcastId': 'b1',
                  'state': 'stopping',
                  'version': 2,
                  'startedAt': '2026-11-09T01:00:00Z',
                },
              }),
              202,
              headers: {'content-type': 'application/json'},
            );
          }
          fail('unexpected ${request.method} $path');
        }),
      );
      final publisher = LivePublisher(api, FakePlatform(attempt));
      await publisher.begin('b1', source: PublishSource.camera);
      expect(whipCalls, 1);
      expect(stopCalls, 1);
      expect(attempt.stopCalls, greaterThan(0));
      expect(publisher.phase, PublishPhase.error);
      expect(publisher.message, contains('송출 연결'));
    },
  );

  test('camera selection is passed to capture and prepare without creating two sessions', () async {
    final capture = Completer<CaptureOffer>()
      ..complete(
        const CaptureOffer(sdp: 'v=0', sharedAudio: false, audioRunning: true),
      );
    final attempt = FakeAttempt(capture);
    final media = FakePlatform(attempt);
    final prepared = Completer<http.Response>();
    final entered = Completer<void>();
    var prepareCalls = 0;
    String? source;
    final api = ApiClient(
      baseUrl: 'https://live.test/api/v1',
      client: MockClient((request) async {
        final path = request.url.path;
        if (path.endsWith('/broadcasts/b1/sessions') &&
            request.method == 'POST') {
          prepareCalls++;
          source =
              (jsonDecode(request.body) as Map<String, dynamic>)['source']
                  as String;
          entered.complete();
          return prepared.future;
        }
        if (path.endsWith('/broadcasts/b1/sessions/s1/stop')) {
          return http.Response(
            jsonEncode({
              'session': {
                'id': 's1',
                'broadcastId': 'b1',
                'state': 'stopping',
                'version': 2,
                'startedAt': '2026-11-09T01:00:00Z',
              },
            }),
            202,
            headers: {'content-type': 'application/json'},
          );
        }
        fail('unexpected ${request.method} $path');
      }),
    );
    final publisher = LivePublisher(api, media);
    final work = publisher.begin('b1', source: PublishSource.camera);
    await entered.future;
    expect(media.sources, [PublishSource.camera]);
    expect(source, 'camera');
    await publisher.stop();
    prepared.complete(
      http.Response(
        jsonEncode({
          'session': {
            'id': 's1',
            'broadcastId': 'b1',
            'state': 'starting',
            'version': 1,
            'startedAt': '2026-11-09T01:00:00Z',
          },
          'publishGrant': null,
        }),
        202,
        headers: {'content-type': 'application/json'},
      ),
    );
    await work;
    expect(prepareCalls, 1);
  });

  test('logout hook stops active capture before sending logout', () async {
    final capture = Completer<CaptureOffer>();
    final attempt = FakeAttempt(capture);
    var logoutCalls = 0, prepareCalls = 0;
    final api = ApiClient(
      baseUrl: 'https://live.test/api/v1',
      client: MockClient((request) async {
        final path = request.url.path;
        if (path.endsWith('/auth/login')) {
          return http.Response(
            jsonEncode({
              'accessToken': 'access',
              'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'A'},
            }),
            200,
            headers: {'content-type': 'application/json'},
          );
        }
        if (path.endsWith('/auth/logout')) {
          expect(attempt.stopCalls, greaterThan(0));
          logoutCalls++;
          return http.Response('', 204);
        }
        if (path.endsWith('/sessions')) prepareCalls++;
        fail('unexpected ${request.method} $path');
      }),
    );
    final publisher = LivePublisher(api, FakePlatform(attempt));
    final auth = AuthController(api, beforeLogout: publisher.stop);
    await auth.login('a@b.c', 'pw');
    final publishing = publisher.begin('b1');
    await auth.logout();
    expect(logoutCalls, 1);
    expect(attempt.stopCalls, greaterThan(0));
    capture.completeError(Exception('permission cancelled'));
    await publishing;
    expect(prepareCalls, 0);
  });

  test('duplicate begin while capture is pending invokes one capture and no server session', () async {
    final capture = Completer<CaptureOffer>();
    final attempt = FakeAttempt(capture);
    var apiCalls = 0;
    final api = ApiClient(
      baseUrl: 'https://live.test/api/v1',
      client: MockClient((request) async {
        apiCalls++;
        fail('unexpected ${request.method} ${request.url}');
      }),
    );
    final publisher = LivePublisher(api, FakePlatform(attempt));
    final first = publisher.begin('b1');
    await publisher.begin('b1');
    expect(attempt.captureCalls, 1);
    expect(apiCalls, 0);
    await publisher.stop();
    capture.completeError(Exception('cancelled'));
    await first;
    expect(attempt.stopCalls, greaterThanOrEqualTo(1));
  });

  test('late WHIP 201 after stop deletes only old resource while new attempt survives', () async {
    final aCapture = Completer<CaptureOffer>()
      ..complete(
        const CaptureOffer(sdp: 'v=0', sharedAudio: true, audioRunning: true),
      );
    final bCapture = Completer<CaptureOffer>();
    final a = FakeAttempt(aCapture), b = FakeAttempt(bCapture);
    final whip = Completer<http.Response>();
    final entered = Completer<void>();
    final paths = <String>[];
    final api = ApiClient(
      baseUrl: 'https://live.test/api/v1',
      client: MockClient((request) async {
        final path = request.url.path;
        paths.add('${request.method} $path');
        if (path.endsWith('/broadcasts/a/sessions') &&
            request.method == 'POST') {
          return http.Response(
            jsonEncode({
              'session': {
                'id': 's1',
                'broadcastId': 'a',
                'state': 'starting',
                'version': 1,
                'startedAt': '2026-11-09T01:00:00Z',
              },
              'publishGrant': {
                'token': 'grant',
                'whipUrl': '/api/v1/live-sessions/s1/whip',
                'expiresAt': '2026-11-09T01:00:30Z',
              },
            }),
            202,
            headers: {'content-type': 'application/json'},
          );
        }
        if (path.endsWith('/live-sessions/s1/whip') &&
            request.method == 'POST') {
          entered.complete();
          return whip.future;
        }
        if (path.endsWith('/broadcasts/a/sessions/s1/stop')) {
          return http.Response(
            jsonEncode({
              'session': {
                'id': 's1',
                'broadcastId': 'a',
                'state': 'stopping',
                'version': 2,
                'startedAt': '2026-11-09T01:00:00Z',
              },
            }),
            202,
            headers: {'content-type': 'application/json'},
          );
        }
        if (path.endsWith('/live-sessions/s1/whip/r1') &&
            request.method == 'DELETE') {
          return http.Response('', 204);
        }
        fail('unexpected ${request.method} $path');
      }),
    );
    final publisher = LivePublisher(api, QueuePlatform([a, b]));
    final old = publisher.begin('a');
    await entered.future;
    await publisher.stop();
    final current = publisher.begin('b');
    expect(publisher.phase, PublishPhase.capturing);
    whip.complete(
      http.Response(
        'v=0\r\nanswer',
        201,
        headers: {
          'content-type': 'application/sdp',
          'location': '/api/v1/live-sessions/s1/whip/r1',
        },
      ),
    );
    await old;
    expect(
      paths.where((x) => x == 'DELETE /api/v1/live-sessions/s1/whip/r1'),
      hasLength(1),
    );
    expect(paths.where((x) => x.contains('/broadcasts/b/sessions')), isEmpty);
    expect(publisher.phase, PublishPhase.capturing);
    expect(publisher.broadcastId, 'b');
    expect(b.stopCalls, 0);
    await publisher.stop();
    bCapture.completeError(Exception('cancelled'));
    await current;
  });

  test(
    'capture starts synchronously and rejected permission makes no session',
    () async {
      final capture = Completer<CaptureOffer>();
      final attempt = FakeAttempt(capture);
      var sessionCalls = 0;
      final api = ApiClient(
        baseUrl: 'https://live.test/api/v1',
        client: MockClient((request) async {
          if (request.url.path.endsWith('/sessions')) sessionCalls++;
          fail('unexpected ${request.method} ${request.url}');
        }),
      );
      final publisher = LivePublisher(api, FakePlatform(attempt));
      final work = publisher.begin('b1');
      expect(attempt.captureCalls, 1);
      capture.completeError(Exception('NotAllowedError'));
      await work;
      expect(sessionCalls, 0);
      expect(attempt.stopCalls, 1);
    },
  );

  test(
    'late prepare after stop only stops its own session and never POSTs WHIP',
    () async {
      final capture = Completer<CaptureOffer>()
        ..complete(
          const CaptureOffer(sdp: 'v=0', sharedAudio: true, audioRunning: true),
        );
      final attempt = FakeAttempt(capture);
      final prepared = Completer<http.Response>();
      final entered = Completer<void>();
      final paths = <String>[];
      final api = ApiClient(
        baseUrl: 'https://live.test/api/v1',
        client: MockClient((request) async {
          paths.add('${request.method} ${request.url.path}');
          if (request.url.path.endsWith('/sessions') &&
              request.method == 'POST') {
            entered.complete();
            return prepared.future;
          }
          if (request.url.path.endsWith('/sessions/s1/stop')) {
            return http.Response(
              jsonEncode({
                'session': {
                  'id': 's1',
                  'broadcastId': 'b1',
                  'state': 'stopping',
                  'version': 2,
                  'startedAt': '2026-11-09T01:00:00Z',
                },
              }),
              202,
            );
          }
          fail('unexpected ${request.method} ${request.url}');
        }),
      );
      final publisher = LivePublisher(api, FakePlatform(attempt));
      final work = publisher.begin('b1');
      await entered.future;
      final stopped = publisher.stop();
      prepared.complete(
        http.Response(
          jsonEncode({
            'session': {
              'id': 's1',
              'broadcastId': 'b1',
              'state': 'starting',
              'version': 1,
              'startedAt': '2026-11-09T01:00:00Z',
            },
            'publishGrant': {
              'token': 'private',
              'whipUrl': '/api/v1/live-sessions/s1/whip',
              'expiresAt': '2026-11-09T01:00:30Z',
            },
          }),
          202,
        ),
      );
      await work;
      await stopped;
      expect(paths.where((e) => e.contains('/whip')), isEmpty);
      expect(paths.where((e) => e.endsWith('/sessions/s1/stop')).length, 1);
      expect(attempt.stopCalls, 1);
    },
  );

  test(
    'failed cleanup of late A prepare cannot replace B capture state',
    () async {
      final aCapture = Completer<CaptureOffer>()
        ..complete(
          const CaptureOffer(sdp: 'v=0', sharedAudio: true, audioRunning: true),
        );
      final bCapture = Completer<CaptureOffer>();
      final aPrepared = Completer<http.Response>();
      final entered = Completer<void>();
      final api = ApiClient(
        baseUrl: 'https://live.test/api/v1',
        client: MockClient((request) async {
          if (request.url.path.endsWith('/broadcasts/a/sessions') &&
              request.method == 'POST') {
            entered.complete();
            return aPrepared.future;
          }
          if (request.url.path.endsWith('/broadcasts/a/sessions/s1/stop')) {
            return http.Response(
              jsonEncode({
                'error': {'code': 'control_failed', 'message': 'failed'},
              }),
              500,
            );
          }
          fail('unexpected ${request.method} ${request.url}');
        }),
      );
      final publisher = LivePublisher(
        api,
        QueuePlatform([FakeAttempt(aCapture), FakeAttempt(bCapture)]),
      );
      final old = publisher.begin('a');
      await entered.future;
      await publisher.stop();
      final current = publisher.begin('b');
      expect(publisher.phase, PublishPhase.capturing);
      aPrepared.complete(
        http.Response(
          jsonEncode({
            'session': {
              'id': 's1',
              'broadcastId': 'a',
              'state': 'starting',
              'version': 1,
              'startedAt': '2026-11-09T01:00:00Z',
            },
            'publishGrant': null,
          }),
          202,
        ),
      );
      await old;
      expect(publisher.phase, PublishPhase.capturing);
      expect(publisher.broadcastId, 'b');
      await publisher.stop();
      bCapture.completeError(Exception('cancelled'));
      await current;
    },
  );
}
