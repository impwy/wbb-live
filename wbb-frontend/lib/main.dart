import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'api/wbb_api.dart';
import 'screens/home_screen.dart';
import 'state/auth_controller.dart';

void main() {
  runApp(const WbbApp());
}

class WbbApp extends StatelessWidget {
  const WbbApp({super.key, this.apiClient});

  /// Injectable for widget tests; production uses a default client.
  final ApiClient? apiClient;

  @override
  Widget build(BuildContext context) {
    final client = apiClient ?? ApiClient();
    return MultiProvider(
      providers: [
        Provider<ApiClient>.value(value: client),
        ChangeNotifierProvider(
          create: (_) => AuthController(client)..restore(),
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
        home: const HomeScreen(),
      ),
    );
  }
}
