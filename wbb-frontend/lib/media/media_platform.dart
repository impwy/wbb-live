import 'package:flutter/widgets.dart';

class CaptureOffer {
  const CaptureOffer({
    required this.sdp,
    required this.sharedAudio,
    required this.audioRunning,
  });
  final String sdp;
  final bool sharedAudio;
  final bool audioRunning;
}

/// Browser resources belong to one attempt, including its source tracks,
/// mixed destination, AudioContext, preview and PeerConnection.
abstract class PublisherMediaAttempt {
  Widget get preview;
  Future<CaptureOffer> captureAndOffer();
  Future<void> acceptAnswer(String answerSdp);
  Future<bool> resumeAudio();

  /// Stops tracks and the PC synchronously before this Future completes.
  Future<void> stopLocal();
}

abstract class ViewerMediaPlayer {
  Widget get view;
  Future<void> play({required bool withSound});
  void goLive();
  void dispose();
}

abstract class MediaPlatform {
  PublisherMediaAttempt newPublisher(void Function() onDisplayEnded);
  ViewerMediaPlayer newViewer(String masterUrl, void Function(String) onEvent);
}
