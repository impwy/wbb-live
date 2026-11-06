// Platform-conditional HTTP client factory. Import this, never
// browser_client directly, so VM tests don't compile package:web.
export 'http_client_io.dart' if (dart.library.js_interop) 'http_client_web.dart';
