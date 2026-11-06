/// API base URL. Same-origin by default (Caddy serves both in production);
/// pass --dart-define=WBB_API_BASE_URL=http://localhost:8080/api/v1 for
/// `flutter run` against a local API.
const String kApiBaseUrl = String.fromEnvironment(
  'WBB_API_BASE_URL',
  defaultValue: '/api/v1',
);
