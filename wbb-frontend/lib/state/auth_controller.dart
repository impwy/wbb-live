import 'package:flutter/foundation.dart';

import '../api/wbb_api.dart';
import '../models/user.dart';

enum AuthStatus { unknown, unauthenticated, authenticated }

/// App-wide authentication state. The access token stays inside the ApiClient
/// (memory only); this notifier exposes the signed-in user.
class AuthController extends ChangeNotifier {
  AuthController(this._api) {
    _api.onSessionExpired = _handleSessionExpired;
  }

  final ApiClient _api;

  AuthStatus status = AuthStatus.unknown;
  User? user;

  /// True right after a live session expired (refresh returned 401). The UI
  /// shows a re-login notice once and calls [clearSessionExpired].
  bool sessionExpired = false;

  bool _restoring = false;

  /// Restore the session on app start using the HttpOnly refresh cookie.
  /// Terminates in every case: authenticated or unauthenticated.
  Future<void> restore() async {
    _restoring = true;
    try {
      final result = await _api.refresh();
      user = result.user;
      status = AuthStatus.authenticated;
    } on ApiException {
      _handleSessionExpired(); // no banner during startup restore
    } finally {
      _restoring = false;
      notifyListeners();
    }
  }

  Future<void> login(String email, String password) async {
    final result = await _api.login(email, password);
    user = result.user;
    status = AuthStatus.authenticated;
    sessionExpired = false;
    notifyListeners();
  }

  /// Register does not auto-login (API contract); sign in afterwards.
  Future<void> register(String email, String password, String displayName) async {
    await _api.register(email: email, password: password, displayName: displayName);
    await login(email, password);
  }

  /// Logout. On failure the exception propagates and the signed-in state is
  /// kept so the user can retry (server-side session is still alive).
  Future<void> logout() async {
    await _api.logout();
    user = null;
    status = AuthStatus.unauthenticated;
    notifyListeners();
  }

  void clearSessionExpired() {
    if (sessionExpired) {
      sessionExpired = false;
      notifyListeners();
    }
  }

  void _handleSessionExpired() {
    user = null;
    status = AuthStatus.unauthenticated;
    if (!_restoring) sessionExpired = true;
    notifyListeners();
  }
}
