import 'media_platform.dart';

MediaPlatform createMediaPlatform() => _UnsupportedMedia();

class _UnsupportedMedia implements MediaPlatform {
  @override
  PublisherMediaAttempt newPublisher(
    PublishSource source,
    void Function() onCaptureEnded,
  ) => throw UnsupportedError('Browser capture requires Flutter Web');

  @override
  ViewerMediaPlayer newViewer(
    String masterUrl,
    void Function(String) onEvent,
  ) => throw UnsupportedError('Browser HLS requires Flutter Web');
}
