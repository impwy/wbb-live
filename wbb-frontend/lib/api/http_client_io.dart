import 'package:http/http.dart' as http;

/// Non-web platforms use the plain client (never used in production: WBB is
/// Flutter Web only, but tests run on the VM).
http.Client createPlatformClient() => http.Client();
