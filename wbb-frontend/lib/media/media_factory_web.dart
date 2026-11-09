import 'dart:convert';
import 'dart:js_interop';
import 'dart:ui_web' as ui_web;

import 'package:flutter/widgets.dart';
import 'package:web/web.dart' as web;

import 'media_platform.dart';

@JS('WbbMedia.newPublisher')
external JSString _newPublisher(JSFunction onEnded);
@JS('WbbMedia.publisherVideo')
external web.HTMLVideoElement _publisherVideo(JSString id);
@JS('WbbMedia.captureOffer')
external JSPromise<JSString> _captureOffer(JSString id);
@JS('WbbMedia.acceptAnswer')
external JSPromise<JSAny?> _acceptAnswer(JSString id, JSString sdp);
@JS('WbbMedia.stopPublisher')
external JSPromise<JSAny?> _stopPublisher(JSString id);
@JS('WbbMedia.resumeAudio')
external JSPromise<JSBoolean> _resumeAudio(JSString id);
@JS('WbbMedia.newViewer')
external JSString _newViewer(JSString url, JSFunction onEvent);
@JS('WbbMedia.viewerVideo')
external web.HTMLVideoElement _viewerVideo(JSString id);
@JS('WbbMedia.playViewer')
external JSPromise<JSAny?> _playViewer(JSString id, JSBoolean withSound);
@JS('WbbMedia.goLive')
external void _goLive(JSString id);
@JS('WbbMedia.stopViewer')
external void _stopViewer(JSString id);

MediaPlatform createMediaPlatform() => _BrowserMedia();

class _BrowserMedia implements MediaPlatform {
  @override
  PublisherMediaAttempt newPublisher(void Function() onDisplayEnded) =>
      _BrowserPublisher(onDisplayEnded);

  @override
  ViewerMediaPlayer newViewer(
    String masterUrl,
    void Function(String) onEvent,
  ) => _BrowserViewer(masterUrl, onEvent);
}

class _BrowserPublisher implements PublisherMediaAttempt {
  _BrowserPublisher(void Function() onDisplayEnded) {
    _callback = (() {
      onDisplayEnded();
    }).toJS;
    _id = _newPublisher(_callback);
    _viewType = 'wbb-preview-${_id.toDart}';
    ui_web.platformViewRegistry.registerViewFactory(
      _viewType,
      (int _) => _publisherVideo(_id),
    );
  }

  late final JSFunction _callback;
  late final JSString _id;
  late final String _viewType;

  @override
  Widget get preview => HtmlElementView(viewType: _viewType);

  @override
  Future<CaptureOffer> captureAndOffer() async {
    final data = jsonDecode(
      (await _captureOffer(_id).toDart).toDart,
    ) as Map<String, dynamic>;
    return CaptureOffer(
      sdp: data['sdp'] as String,
      sharedAudio: data['sharedAudio'] == true,
      audioRunning: data['audioRunning'] == true,
    );
  }

  @override
  Future<void> acceptAnswer(String answerSdp) async {
    await _acceptAnswer(_id, answerSdp.toJS).toDart;
  }

  @override
  Future<void> stopLocal() async {
    await _stopPublisher(_id).toDart;
  }

  @override
  Future<bool> resumeAudio() async => (await _resumeAudio(_id).toDart).toDart;
}

class _BrowserViewer implements ViewerMediaPlayer {
  _BrowserViewer(String masterUrl, void Function(String) onEvent) {
    _callback = ((JSString event) {
      onEvent(event.toDart);
    }).toJS;
    _id = _newViewer(masterUrl.toJS, _callback);
    _viewType = 'wbb-viewer-${_id.toDart}';
    ui_web.platformViewRegistry.registerViewFactory(
      _viewType,
      (int _) => _viewerVideo(_id),
    );
  }

  late final JSFunction _callback;
  late final JSString _id;
  late final String _viewType;

  @override
  Widget get view => HtmlElementView(viewType: _viewType);

  @override
  Future<void> play({required bool withSound}) async {
    await _playViewer(_id, withSound.toJS).toDart;
  }

  @override
  void goLive() => _goLive(_id);

  @override
  void dispose() => _stopViewer(_id);
}
