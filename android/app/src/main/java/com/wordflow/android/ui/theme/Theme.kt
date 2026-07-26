package com.wordflow.android.ui.theme

import android.app.Activity
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat

// WordFlow brand colors
val DarkSlate = Color(0xFF2C3E50)
val WetAsphalt = Color(0xFF34495E)
val Clouds = Color(0xFFECF0F1)
val Silver = Color(0xFFBDC3C7)
val Concrete = Color(0xFF95A5A6)
val Turquoise = Color(0xFF1ABC9C)
val GreenSea = Color(0xFF16A085)
val PeterRiver = Color(0xFF3498DB)
val Alizarin = Color(0xFFE74C3C)
val Carrot = Color(0xFFE67E22)
val SunFlower = Color(0xFFF1C40F)
val Emerald = Color(0xFF2ECC71)
val Nephritis = Color(0xFF27AE60)

// Rating colors
val AgainColor = Color(0xFFE74C3C)
val HardColor = Color(0xFFE67E22)
val GoodColor = Color(0xFF27AE60)
val EasyColor = Color(0xFF3498DB)

private val DarkColorScheme = darkColorScheme(
    primary = Turquoise,
    onPrimary = Color.White,
    secondary = PeterRiver,
    onSecondary = Color.White,
    tertiary = Emerald,
    background = Color(0xFF1A1A2E),
    surface = Color(0xFF16213E),
    onBackground = Clouds,
    onSurface = Clouds,
    error = Alizarin,
)

private val LightColorScheme = lightColorScheme(
    primary = DarkSlate,
    onPrimary = Color.White,
    secondary = Turquoise,
    onSecondary = Color.White,
    tertiary = GreenSea,
    background = Color(0xFFF5F6F8),
    surface = Color.White,
    onBackground = DarkSlate,
    onSurface = DarkSlate,
    error = Alizarin,
)

@Composable
fun WordFlowTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit
) {
    val colorScheme = if (darkTheme) DarkColorScheme else LightColorScheme

    MaterialTheme(
        colorScheme = colorScheme,
        content = content
    )
}
