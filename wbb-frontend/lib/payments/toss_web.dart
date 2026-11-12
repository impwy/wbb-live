import 'dart:convert';
import 'dart:js_interop';

@JS('WbbPay.request')
external JSPromise<JSAny?> _request(JSString clientKey, JSString order);

Future<void> requestTossPayment(
  String clientKey,
  Map<String, Object> order,
) async {
  await _request(clientKey.toJS, jsonEncode(order).toJS).toDart;
}
