import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'api/wbb_api.dart';
import 'media/live_publisher.dart';
import 'media/media_factory.dart';
import 'media/media_platform.dart';
import 'screens/home_screen.dart';
import 'screens/google_complete_screen.dart';
import 'state/auth_controller.dart';

void main() {
  runApp(const WbbApp());
}

class WbbApp extends StatefulWidget {
  const WbbApp({
    super.key,
    this.apiClient,
    this.initialUri,
    this.mediaPlatform,
  });

  /// Injectable for widget tests; production uses a default client.
  final ApiClient? apiClient;
  final Uri? initialUri;
  final MediaPlatform? mediaPlatform;

  @override
  State<WbbApp> createState() => _WbbAppState();
}

class _WbbAppState extends State<WbbApp> {
  late final ApiClient _client = widget.apiClient ?? ApiClient();
  late final MediaPlatform _media =
      widget.mediaPlatform ?? createMediaPlatform();
  late final LivePublisher _publisher = LivePublisher(_client, _media);

  @override
  Widget build(BuildContext context) {
    return MultiProvider(
      providers: [
        Provider<ApiClient>.value(value: _client),
        Provider<MediaPlatform>.value(value: _media),
        ChangeNotifierProvider<LivePublisher>.value(value: _publisher),
        ChangeNotifierProvider(
          create: (_) => AuthController(
            _client,
            beforeLogout: _publisher.stop,
            onExpired: () => unawaited(_publisher.stop()),
          )..restore(),
        ),
      ],
      child: MaterialApp(
        title: 'WBB',
        theme: ThemeData(
          colorSchemeSeed: const Color(0xFF7C4DFF),
          useMaterial3: true,
          brightness: Brightness.dark,
          // Bundled Noto Sans KR (OFL) so text never falls back to CDN fonts.
          fontFamily: 'NotoSansKR',
        ),
        home: (widget.initialUri ?? Uri.base).path == '/auth/google/complete'
            ? GoogleCompleteScreen(
                error: (widget.initialUri ?? Uri.base).queryParameters['error'],
              )
            : const HomeScreen(),
      ),
    );
  }
}
