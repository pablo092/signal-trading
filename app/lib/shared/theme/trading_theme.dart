import 'package:flutter/material.dart';

/// TradingTheme — design system for the trading dashboard.
/// Trading conventions: green = profit/buy, red = loss/sell.
abstract class TradingTheme {
  // ── Brand colors ─────────────────────────────────────────
  static const Color primary = Color(0xFF1A1D2E);       // Deep navy
  static const Color surface = Color(0xFF242736);
  static const Color surfaceVariant = Color(0xFF2D3047);
  static const Color onSurface = Color(0xFFE8EAF6);
  static const Color onSurfaceMuted = Color(0xFF8B8FA8);

  // ── Semantic trading colors ──────────────────────────────
  static const Color profit = Color(0xFF00C896);    // Green — BUY / gain
  static const Color loss = Color(0xFFFF4757);      // Red — SELL / loss
  static const Color neutral = Color(0xFFFFB347);   // Amber — HOLD
  static const Color accent = Color(0xFF7C83FD);    // Purple — highlights

  // ── Signal direction colors ──────────────────────────────
  static Color directionColor(String direction) => switch (direction.toUpperCase()) {
    'BUY' => profit,
    'SELL' => loss,
    _ => neutral,
  };

  static Color confidenceColor(double confidence) {
    if (confidence >= 0.75) return profit;
    if (confidence >= 0.50) return neutral;
    return loss;
  }

  // ── Typography ───────────────────────────────────────────
  static const TextStyle displayLarge = TextStyle(
    fontSize: 32, fontWeight: FontWeight.w700,
    color: onSurface, letterSpacing: -0.5,
  );
  static const TextStyle titleMedium = TextStyle(
    fontSize: 16, fontWeight: FontWeight.w600, color: onSurface,
  );
  static const TextStyle bodyMedium = TextStyle(
    fontSize: 14, fontWeight: FontWeight.w400, color: onSurface,
  );
  static const TextStyle caption = TextStyle(
    fontSize: 12, fontWeight: FontWeight.w400, color: onSurfaceMuted,
  );
  static const TextStyle monospace = TextStyle(
    fontSize: 13, fontFamily: 'monospace', color: onSurface,
  );

  // ── ThemeData ────────────────────────────────────────────
  static ThemeData get darkTheme => ThemeData(
        useMaterial3: true,
        brightness: Brightness.dark,
        colorScheme: ColorScheme.dark(
          primary: accent,
          surface: surface,
          onSurface: onSurface,
          error: loss,
        ),
        scaffoldBackgroundColor: primary,
        cardTheme: CardTheme(
          color: surface,
          elevation: 0,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(16),
          ),
          margin: EdgeInsets.zero,
        ),
        appBarTheme: const AppBarTheme(
          backgroundColor: primary,
          elevation: 0,
          titleTextStyle: titleMedium,
          iconTheme: IconThemeData(color: onSurface),
        ),
        dividerTheme: const DividerThemeData(
          color: surfaceVariant,
          thickness: 1,
          space: 0,
        ),
        textTheme: const TextTheme(
          displayLarge: displayLarge,
          titleMedium: titleMedium,
          bodyMedium: bodyMedium,
          bodySmall: caption,
        ),
        filledButtonTheme: FilledButtonThemeData(
          style: FilledButton.styleFrom(
            backgroundColor: accent,
            foregroundColor: onSurface,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(12),
            ),
            padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 14),
          ),
        ),
        inputDecorationTheme: InputDecorationTheme(
          filled: true,
          fillColor: surfaceVariant,
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(12),
            borderSide: BorderSide.none,
          ),
          labelStyle: caption,
          hintStyle: caption,
        ),
      );
}
