package com.wordflow.android.ui.components

import android.media.MediaPlayer
import android.speech.tts.TextToSpeech
import android.util.Log
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.VolumeUp
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import java.util.Locale

private const val TAG = "AudioBtn"

/**
 * Plays word pronunciation.
 *
 * Strategy:
 * 1. Try Android TTS (works offline, low latency).
 * 2. If TTS engine is unavailable, use Youdao TTS (internet required).
 */
@Composable
fun AudioButton(text: String, modifier: Modifier = Modifier) {
    val context = androidx.compose.ui.platform.LocalContext.current
    var engine by remember { mutableStateOf<TextToSpeech?>(null) }

    DisposableEffect(Unit) {
        onDispose {
            engine?.stop()
            engine?.shutdown()
        }
    }

    IconButton(
        onClick = {
            val word = text.trim().lowercase()
            if (word.isEmpty()) return@IconButton

            // Priority 1: Android TTS (works offline)
            if (engine != null) {
                speakNow(engine!!, word)
                return@IconButton
            }

            // Priority 2: create TTS lazily
            try {
                lateinit var tts: TextToSpeech
                tts = TextToSpeech(context) { status ->
                    if (status == TextToSpeech.SUCCESS) {
                        engine = tts
                        speakNow(tts, word)
                    } else {
                        Log.w(TAG, "TTS unavailable (status=$status), using Youdao TTS")
                        playYoudao(word)
                    }
                }
            } catch (_: Exception) {
                playYoudao(word)
            }
        },
        modifier = modifier,
    ) {
        Icon(Icons.Default.VolumeUp, contentDescription = "播放发音")
    }
}

/** Play Youdao TTS. type=1 = US accent. */
private fun playYoudao(word: String) {
    val url = "https://dict.youdao.com/dictvoice?audio=${java.net.URLEncoder.encode(word, "UTF-8")}&type=1"
    try {
        val mp = MediaPlayer()
        mp.setOnPreparedListener { it.start() }
        mp.setOnCompletionListener { it.release() }
        mp.setOnErrorListener { _, _, _ -> true }
        mp.setDataSource(url)
        mp.prepareAsync()
    } catch (_: Exception) {
        // give up silently
    }
}

/** Speak via Android TTS. Falls back to Youdao if language data is missing. */
private fun speakNow(tts: TextToSpeech, word: String) {
    val langResult = tts.setLanguage(Locale.US)
    when (langResult) {
        TextToSpeech.LANG_MISSING_DATA, TextToSpeech.LANG_NOT_SUPPORTED -> {
            playYoudao(word)
        }
        TextToSpeech.LANG_COUNTRY_AVAILABLE, TextToSpeech.LANG_AVAILABLE -> {
            tts.speak(word, TextToSpeech.QUEUE_FLUSH, null, "wordflow_${word.hashCode()}")
        }
        else -> {
            playYoudao(word)
        }
    }
}
