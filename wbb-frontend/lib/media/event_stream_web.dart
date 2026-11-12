import 'dart:js_interop';

import 'package:web/web.dart' as web;

import 'event_stream.dart';

class _BrowserEventStream implements EventStream {
  _BrowserEventStream(this._source);
  final web.EventSource _source;

  @override
  void close() => _source.close();
}

EventStream openEventStream(
  String url, {
  required void Function(String data) onData,
  required void Function(bool connected) onState,
}) {
  final source = web.EventSource(url);
  source.onopen = ((web.Event _) => onState(true)).toJS;
  source.onerror = ((web.Event _) => onState(false)).toJS;
  source.onmessage = ((web.MessageEvent e) {
    final data = e.data;
    if (data.isA<JSString>()) onData((data as JSString).toDart);
  }).toJS;
  return _BrowserEventStream(source);
}
