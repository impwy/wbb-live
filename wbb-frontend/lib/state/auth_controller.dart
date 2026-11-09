import 'package:flutter/foundation.dart';

import '../api/wbb_api.dart';
import '../models/user.dart';

enum AuthStatus { unknown, unauthenticated, authenticated }

/// App-wide authentication state. The access token stays inside the ApiClient
/// (memory only); this notifier exposes the signed-in user.
class AuthController extends ChangeNotifier {
  AuthController(this._api, {this.beforeLogout, this.onExpired}) {
    _api.onSessionExpired = _handleSessionExpired;
  }

  final ApiClient _api;
  final Future<void> Function()? beforeLogout;
  final void Function()? onExpired;

  AuthStatus status = AuthStatus.unknown;
  User? user;

  /// True right after a live session expired (refresh returned 401). The UI
  /// shows a re-login notice once and calls [clearSessionExpired].
  bool sessionExpired = false;

  bool _restoring = false;
  int _intentEpoch = 0;

  /// Restore the session on app start using the HttpOnly refresh cookie.
  /// Terminates in every case: authenticated or unauthenticated.
  Future<void> restore() async {
    final epoch = ++_intentEpoch;
    _restoring = true;
    try {
      final result = await _api.refresh();
      if (epoch != _intentEpoch) return;
      user = result.user;
      status = AuthStatus.authenticated;
    } on ApiException {
      if (epoch == _intentEpoch) _handleSessionExpired();
    } finally {
      if (epoch == _intentEpoch) {
        _restoring = false;
        notifyListeners();
      }
    }
  }

  Future<void> login(String email, String password) async {
    final epoch = ++_intentEpoch;
    _restoring = false;
    final result = await _api.login(email, password);
    if (epoch != _intentEpoch) return;
    user = result.user;
    status = AuthStatus.authenticated;
    sessionExpired = false;
    notifyListeners();
  }

  /// Register does not auto-login (API contract); sign in afterwards.
  Future<void> register(
    String email,
    String password,
    String displayName,
  ) async {
    await _api.register(
      email: email,
      password: password,
      displayName: displayName,
    );
    await login(email, password);
  }

  /// Logout. On failure the exception propagates and the signed-in state is
  /// kept so the user can retry (server-side session is still alive).
  Future<void> logout() async {
    final epoch = ++_intentEpoch;
    try {
      await beforeLogout?.call().timeout(const Duration(seconds: 3));
    } catch (_) {
      // Local resources were stopped synchronously. Server family revocation
      // on logout remains the cleanup authority if signaling is stuck.
    }
    await _api.logout();
    if (epoch != _intentEpoch) return;
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
    _intentEpoch++;
    onExpired?.call();
    final showBanner = !_restoring;
    _restoring = false;
    user = null;
    status = AuthStatus.unauthenticated;
    if (showBanner) sessionExpired = true;
    notifyListeners();
  }
}
