package com.wordflow.android.ui.theme

import android.content.Context
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue

/**
 * Persisted appearance preferences (own SharedPreferences, does not touch the data layer).
 * - darkMode: 0 = follow system, 1 = light, 2 = dark
 * - dynamicColor: Material You on Android 12+ (off by default for a consistent brand identity)
 */
class ThemeState internal constructor(private val prefs: android.content.SharedPreferences) {
    var dynamicColor by mutableStateOf(prefs.getBoolean(KEY_DYNAMIC, false))
        private set
    var darkMode by mutableStateOf(prefs.getInt(KEY_DARK, MODE_SYSTEM))
        private set

    fun updateDynamicColor(value: Boolean) {
        prefs.edit().putBoolean(KEY_DYNAMIC, value).apply()
        dynamicColor = value
    }

    fun updateDarkMode(value: Int) {
        prefs.edit().putInt(KEY_DARK, value).apply()
        darkMode = value
    }

    companion object {
        private const val PREFS = "wordflow_ui"
        private const val KEY_DYNAMIC = "dynamicColor"
        private const val KEY_DARK = "darkMode"
        const val MODE_SYSTEM = 0
        const val MODE_LIGHT = 1
        const val MODE_DARK = 2

        fun create(context: Context): ThemeState =
            ThemeState(context.getSharedPreferences(PREFS, Context.MODE_PRIVATE))
    }
}
