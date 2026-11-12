import 'ice_servers_stub.dart'
    if (dart.library.js_interop) 'ice_servers_web.dart'
    as implementation;

/// Hands the browser media module a pending ICE server list for the next
/// publish attempt. Non-web builds ignore it without calling [load].
void provideIceServers(Future<String?> Function() load) =>
    implementation.provideIceServers(load);
