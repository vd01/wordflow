package com.wordflow.android.ui.components

import android.content.Context
import android.media.MediaPlayer
import android.speech.tts.TextToSpeech
import android.util.Log
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.VolumeUp
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import java.util.Locale

private const val TAG = "AudioBtn"

/**
 * Speaks word pronunciation.
 *
 * Strategy:
 * 1. Try Android TTS (works offline, low latency).
 * 2. If TTS engine is unavailable, use Youdao TTS (internet required).
 *
 * Owns a lazily-created [TextToSpeech] engine; call [release] when done
 * (e.g. from a DisposableEffect) to stop and shut it down.
 */
class TtsSpeaker(private val context: Context) {
    private var engine: TextToSpeech? = null
    private var initializing = false
    private val pending = mutableListOf<String>()

    /** Speak a word. Safe to call before TTS finishes initializing (queued until ready). */
    fun speak(text: String) {
        val word = text.trim().lowercase()
        if (word.isEmpty()) return

        val e = engine
        if (e != null) {
            speakNow(e, word)
            return
        }
        // Not ready yet: queue the word; a single lazy init will flush everything.
        pending.add(word)
        if (initializing) return
        initializing = true
        try {
            lateinit var tts: TextToSpeech
            tts = TextToSpeech(context) { status ->
                initializing = false
                if (status == TextToSpeech.SUCCESS) {
                    engine = tts
                    val queued = pending.toList()
                    pending.clear()
                    queued.forEach { speakNow(tts, it) }
                } else {
                    Log.w(TAG, "TTS unavailable (status=$status), using Youdao TTS")
                    val last = pending.lastOrNull() ?: word
                    pending.clear()
                    playYoudao(last)
                }
            }
        } catch (_: Exception) {
            initializing = false
            val last = pending.lastOrNull() ?: word
            pending.clear()
            playYoudao(last)
        }
    }

    /** Stop playback and release the TTS engine. */
    fun release() {
        pending.clear()
        initializing = false
        engine?.stop()
        engine?.shutdown()
        engine = null
    }
}

/** Creates a [TtsSpeaker] tied to this composable's lifecycle (released on dispose). */
@Composable
fun rememberTtsSpeaker(): TtsSpeaker {
    val context = LocalContext.current.applicationContext
    val speaker = remember { TtsSpeaker(context) }
    DisposableEffect(Unit) {
        onDispose { speaker.release() }
    }
    return speaker
}

/** Manual pronunciation button. */
@Composable
fun AudioButton(speaker: TtsSpeaker, text: String, modifier: Modifier = Modifier) {
    IconButton(
        onClick = { speaker.speak(text) },
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
