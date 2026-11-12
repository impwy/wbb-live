import 'package:flutter/material.dart';

/// Shared streaming-site visuals: palette, channel avatars and generated
/// thumbnails (there are no live frame thumbnails).
class WbbColors {
  static const background = Color(0xFF0F0F12);
  static const surface = Color(0xFF18181C);
  static const surfaceHigh = Color(0xFF232329);
  static const border = Color(0xFF2E2E36);
  static const accent = Color(0xFF8B5CF6);
  static const live = Color(0xFFE5323B);
  static const muted = Color(0xFFADADB8);
}

ThemeData wbbTheme() {
  final scheme = ColorScheme.fromSeed(
    seedColor: WbbColors.accent,
    brightness: Brightness.dark,
  ).copyWith(
    primary: WbbColors.accent,
    surface: WbbColors.surface,
    surfaceContainerHighest: WbbColors.surfaceHigh,
    outlineVariant: WbbColors.border,
  );
  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    brightness: Brightness.dark,
    // Bundled Noto Sans KR (OFL) so text never falls back to CDN fonts.
    fontFamily: 'NotoSansKR',
    scaffoldBackgroundColor: WbbColors.background,
    appBarTheme: const AppBarTheme(
      backgroundColor: WbbColors.surface,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      toolbarHeight: 52,
      centerTitle: false,
      titleTextStyle: TextStyle(
        fontFamily: 'NotoSansKR',
        fontSize: 18,
        fontWeight: FontWeight.w700,
      ),
      shape: Border(bottom: BorderSide(color: WbbColors.border)),
    ),
    cardTheme: const CardThemeData(
      color: Colors.transparent,
      elevation: 0,
      margin: EdgeInsets.zero,
    ),
    dividerTheme: const DividerThemeData(color: WbbColors.border),
    chipTheme: const ChipThemeData(
      backgroundColor: WbbColors.surfaceHigh,
      side: BorderSide.none,
      shape: StadiumBorder(),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(6)),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(6)),
        side: const BorderSide(color: WbbColors.border),
      ),
    ),
  );
}

/// A stable color pair derived from an id (channel avatar / thumbnail).
(Color, Color) _palette(String seed) {
  var h = 0;
  for (final c in seed.codeUnits) {
    h = (h * 31 + c) & 0x7fffffff;
  }
  final hue = (h % 360).toDouble();
  return (
    HSLColor.fromAHSL(1, hue, 0.55, 0.42).toColor(),
    HSLColor.fromAHSL(1, (hue + 40) % 360, 0.6, 0.22).toColor(),
  );
}

String _initial(String name) =>
    name.trim().isEmpty ? '?' : name.trim().characters.first.toUpperCase();

class ChannelAvatar extends StatelessWidget {
  const ChannelAvatar({
    super.key,
    required this.seed,
    required this.name,
    this.size = 36,
    this.live = false,
  });
  final String seed;
  final String name;
  final double size;
  final bool live;

  @override
  Widget build(BuildContext context) {
    final (a, _) = _palette(seed);
    return Container(
      width: size,
      height: size,
      padding: EdgeInsets.all(live ? 2 : 0),
      decoration: BoxDecoration(
        shape: BoxShape.circle,
        border: live ? Border.all(color: WbbColors.live, width: 2) : null,
      ),
      child: CircleAvatar(
        backgroundColor: a,
        child: Text(
          _initial(name),
          style: TextStyle(
            color: Colors.white,
            fontWeight: FontWeight.w700,
            fontSize: size * 0.4,
          ),
        ),
      ),
    );
  }
}

/// A generated 16:9 thumbnail: gradient, large initial and overlays.
class GeneratedThumbnail extends StatelessWidget {
  const GeneratedThumbnail({
    super.key,
    required this.seed,
    required this.title,
    this.topLeft,
    this.bottomLeft,
    this.dimmed = false,
  });
  final String seed;
  final String title;
  final Widget? topLeft;
  final Widget? bottomLeft;
  final bool dimmed;

  @override
  Widget build(BuildContext context) {
    final (a, b) = _palette(seed);
    return AspectRatio(
      aspectRatio: 16 / 9,
      child: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: Stack(
          fit: StackFit.expand,
          children: [
            DecoratedBox(
              decoration: BoxDecoration(
                gradient: LinearGradient(
                  begin: Alignment.topLeft,
                  end: Alignment.bottomRight,
                  colors: dimmed
                      ? [a.withValues(alpha: 0.35), b.withValues(alpha: 0.35)]
                      : [a, b],
                ),
              ),
            ),
            Center(
              child: Text(
                _initial(title),
                style: TextStyle(
                  color: Colors.white.withValues(alpha: dimmed ? 0.3 : 0.55),
                  fontSize: 56,
                  fontWeight: FontWeight.w800,
                ),
              ),
            ),
            if (topLeft != null) Positioned(left: 8, top: 8, child: topLeft!),
            if (bottomLeft != null)
              Positioned(left: 8, bottom: 8, child: bottomLeft!),
          ],
        ),
      ),
    );
  }
}

/// Small rounded label on top of thumbnails (e.g. replay duration).
class OverlayLabel extends StatelessWidget {
  const OverlayLabel(this.text, {super.key});
  final String text;

  @override
  Widget build(BuildContext context) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
    decoration: BoxDecoration(
      color: Colors.black.withValues(alpha: 0.7),
      borderRadius: BorderRadius.circular(4),
    ),
    child: Text(
      text,
      style: const TextStyle(color: Colors.white, fontSize: 12),
    ),
  );
}
