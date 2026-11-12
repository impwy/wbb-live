import 'event_stream_stub.dart'
    if (dart.library.js_interop) 'event_stream_web.dart'
    as impl;

/// A Server-Sent Events subscription. [onData] receives each message's data;
/// [onState] reports whether the stream is currently connected. The browser
/// reconnects automatically.
abstract class EventStream {
  void close();
}

EventStream openEventStream(
  String url, {
  required void Function(String data) onData,
  required void Function(bool connected) onState,
}) => impl.openEventStream(url, onData: onData, onState: onState);
