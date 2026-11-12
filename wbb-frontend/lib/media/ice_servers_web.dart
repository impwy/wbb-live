import 'dart:js_interop';

@JS('WbbMedia.provideIceServers')
external void _provide(JSPromise<JSString?> pending);

void provideIceServers(Future<String?> Function() load) {
  _provide(load().then((json) => json?.toJS).toJS);
}
