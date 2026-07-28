package com.wordflow.android.ui.components

/** Strip surrounding slashes from a phonetic so the UI wraps it exactly once. */
fun formatPhonetic(raw: String): String {
    val trimmed = raw.trim().trim('/')
    return if (trimmed.isBlank()) "" else "/$trimmed/"
}
