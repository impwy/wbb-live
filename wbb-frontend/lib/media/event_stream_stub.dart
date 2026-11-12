import 'event_stream.dart';

class _Noop implements EventStream {
  @override
  void close() {}
}

EventStream openEventStream(
  String url, {
  required void Function(String data) onData,
  required void Function(bool connected) onState,
}) => _Noop();
