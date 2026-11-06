import 'package:http/browser_client.dart' as http;

/// Web: sends the HttpOnly refresh cookie (withCredentials).
http.BrowserClient createPlatformClient() => http.BrowserClient()..withCredentials = true;
