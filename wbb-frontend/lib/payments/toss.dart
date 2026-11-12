import 'toss_stub.dart' if (dart.library.js_interop) 'toss_web.dart' as impl;

/// Opens the provider's test payment window. On success the browser leaves
/// the app for the success URL, so this only returns on failure to start.
Future<void> requestTossPayment(String clientKey, Map<String, Object> order) =>
    impl.requestTossPayment(clientKey, order);
