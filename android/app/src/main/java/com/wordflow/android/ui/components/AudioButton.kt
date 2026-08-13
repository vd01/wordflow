package com.wordflow.android.ui.components

import android.content.Context
import android.media.MediaPlayer
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.Handler
import android.os.Looper
import android.speech.tts.TextToSpeech
import android.speech.tts.UtteranceProgressListener
import android.util.Log
import android.widget.Toast
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.VolumeUp
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import com.wordflow.android.data.NoAudioCache
import java.net.URLEncoder
import java.util.Locale

private const val TAG = "AudioBtn"

/** Give up on a remote audio fetch after this long, so the user is never left waiting forever. */
private const val YOUDOU_TIMEOUT_MS = 8_000L

/** Give up waiting for the TTS engine to initialize (missing/broken engine). */
private const val TTS_INIT_TIMEOUT_MS = 5_000L

private const val UTTERANCE_PREFIX = "wf::"

/**
 * Speaks word pronunciation.
 *
 * Strategy:
 * 1. Try Android TTS (works offline, low latency).
 * 2. If TTS engine is unavailable, or it fails on a word, use Youdao TTS (internet required).
 *
 * Owns a lazily-created [TextToSpeech] engine; call [release] when done
 * (e.g. from a DisposableEffect) to stop playback, shut the engine down and
 * free any in-flight remote players.
 *
 * Failures are reported through the optional [speak] callback so the UI can
 * prompt the user instead of staying silent. All mutable state is confined to
 * the main thread (TTS callbacks are re-dispatched through the main handler).
 */
class TtsSpeaker(
    private val context: Context,
    private val noAudio: NoAudioCache? = null,
    private val youdaoTimeoutMs: Long = YOUDOU_TIMEOUT_MS,
) {
    private val mainHandler = Handler(Looper.getMainLooper())

    private var engine: TextToSpeech? = null
    private var initializing = false
    private var initWatchdog: Runnable? = null

    /** Words queued while the TTS engine boots; each keeps its own failure callback. */
    private val pending = mutableListOf<Pair<String, ((String) -> Unit)?>>()

    /** Utterance id -> failure callback, so a failed utterance can fall back to Youdao. */
    private val utteranceCallbacks = mutableMapOf<String, ((String) -> Unit)?>()

    /** Remote players still preparing/playing; stopped and released by [release]. */
    private val activePlayers = mutableListOf<YoudaoPlayer>()

    /** A remote audio fetch with its watchdog; main-thread-confined. */
    private class YoudaoPlayer(val player: MediaPlayer, var watchdog: Runnable)

    /** Speak a word. Safe to call before TTS finishes initializing (queued until ready). */
    fun speak(text: String, onFailure: ((String) -> Unit)? = null) {
        val word = text.trim()
        if (word.isEmpty()) return

        val e = engine
        if (e != null) {
            speakNow(e, word, onFailure)
            return
        }
        // Not ready yet: queue the word; a single lazy init will flush everything.
        pending.add(word to onFailure)
        if (initializing) return
        initializing = true
        try {
            lateinit var tts: TextToSpeech
            tts = TextToSpeech(context) { status ->
                mainHandler.post {
                    initWatchdog?.let { mainHandler.removeCallbacks(it) }
                    initWatchdog = null
                    initializing = false
                    if (status == TextToSpeech.SUCCESS) {
                        Log.i(TAG, "TTS engine ready")
                        tts.setOnUtteranceProgressListener(utteranceListener)
                        engine = tts
                        flushPending { w, cb -> speakNow(tts, w, cb) }
                    } else {
                        Log.w(TAG, "TTS unavailable (status=$status), using Youdao TTS")
                        flushPending { w, cb -> playYoudao(w, cb) }
                    }
                }
            }
            // Watchdog: if the TTS engine never finishes initializing (missing or
            // broken engine), stop waiting and fall back to Youdao so the user is
            // never left waiting with no sound at all.
            val watchdog = Runnable {
                Log.w(TAG, "TTS init timed out after ${TTS_INIT_TIMEOUT_MS}ms, using Youdao TTS")
                initializing = false
                initWatchdog = null
                flushPending { w, cb -> playYoudao(w, cb) }
            }
            initWatchdog = watchdog
            mainHandler.postDelayed(watchdog, TTS_INIT_TIMEOUT_MS)
        } catch (_: Exception) {
            initWatchdog?.let { mainHandler.removeCallbacks(it) }
            initWatchdog = null
            initializing = false
            flushPending { w, cb -> playYoudao(w, cb) }
        }
    }

    /** Drain every queued word through [action] (e.g. Youdao playback after TTS init failed). */
    private fun flushPending(action: (String, ((String) -> Unit)?) -> Unit) {
        val queued = pending.toList()
        pending.clear()
        queued.forEach { (w, cb) -> action(w, cb) }
    }

    /** Stop playback and release the TTS engine and any pending remote players. */
    fun release() {
        initWatchdog?.let { mainHandler.removeCallbacks(it) }
        initWatchdog = null
        pending.clear()
        utteranceCallbacks.clear()
        initializing = false
        engine?.stop()
        engine?.shutdown()
        engine = null
        activePlayers.forEach { state ->
            mainHandler.removeCallbacks(state.watchdog)
            runCatching { state.player.stop() }
            runCatching { state.player.release() }
        }
        activePlayers.clear()
    }

    /** Play Youdao TTS. type=1 = US accent. Fails loudly (callback) instead of hanging. */
    private fun playYoudao(word: String, onFailure: ((String) -> Unit)?) {
        // Words Youdao is known not to have audio for: don't retry them, fail fast.
        if (noAudio?.hasNoAudio(word) == true) {
            Log.i(TAG, "Skipping Youdao (known no-audio): $word")
            onFailure?.invoke(word)
            return
        }
        val url = "https://dict.youdao.com/dictvoice?audio=${URLEncoder.encode(word, "UTF-8")}&type=1"
        try {
            // Stop any earlier remote playback so rapid taps don't overlap.
            activePlayers.forEach { state ->
                mainHandler.removeCallbacks(state.watchdog)
                runCatching { state.player.stop() }
                runCatching { state.player.release() }
            }
            activePlayers.clear()

            val mp = MediaPlayer()
            val state = YoudaoPlayer(mp, Runnable {})
            // Watchdog: if the audio never becomes ready (offline, server stall, word
            // unknown to Youdao), abort instead of leaving the user waiting forever.
            val watchdog = Runnable {
                if (activePlayers.contains(state)) {
                    activePlayers.remove(state)
                    Log.w(TAG, "Youdao TTS timeout, giving up: $word")
                    runCatching { mp.release() }
                    onFailure?.invoke(word)
                }
            }
            state.watchdog = watchdog
            activePlayers.add(state)

            mp.setOnPreparedListener { p ->
                mainHandler.removeCallbacks(watchdog)
                if (activePlayers.contains(state)) p.start()
            }
            mp.setOnCompletionListener { p ->
                mainHandler.removeCallbacks(watchdog)
                activePlayers.remove(state)
                runCatching { p.release() }
            }
            mp.setOnErrorListener { p, what, extra ->
                mainHandler.removeCallbacks(watchdog)
                activePlayers.remove(state)
                Log.w(TAG, "Youdao TTS error: $word (what=$what extra=$extra)")
                runCatching { p.release() }
                // Youdao answers missing words with an HTTP 500, so a real error
                // means "no audio" — record it (unless we're offline) so we stop
                // retrying this word. The connectivity check keeps one-off offline
                // moments from blacklisting words that actually have audio.
                if (isOnline(context)) noAudio?.recordNoAudio(word)
                onFailure?.invoke(word)
                true // handled; do not let MediaPlayer run its default error path
            }
            mp.setDataSource(url)
            mp.prepareAsync()
            mainHandler.postDelayed(watchdog, youdaoTimeoutMs)
        } catch (e: Exception) {
            Log.w(TAG, "Youdao TTS start failed: $word", e)
            onFailure?.invoke(word)
        }
    }

    /** Called when the TTS engine reports that an utterance failed. Falls back to Youdao. */
    private fun onUtteranceError(utteranceId: String?) {
        val cb = utteranceId?.let { utteranceCallbacks.remove(it) } ?: return
        val word = utteranceId.removePrefix(UTTERANCE_PREFIX)
        if (word.isEmpty()) {
            Log.w(TAG, "TTS utterance error with empty word id: $utteranceId")
            return
        }
        Log.w(TAG, "TTS utterance error, falling back to Youdao: $word")
        playYoudao(word, cb)
    }

    @Suppress("DEPRECATION") // onError(String) is deprecated on API 21+ but still required for older APIs
    private val utteranceListener = object : UtteranceProgressListener() {
        override fun onStart(utteranceId: String?) = Unit
        override fun onDone(utteranceId: String?) {
            utteranceId?.let { utteranceCallbacks.remove(it) }
        }
        @Deprecated("Deprecated in Java")
        override fun onError(utteranceId: String?) = onUtteranceError(utteranceId)
        override fun onError(utteranceId: String?, errorCode: Int) = onUtteranceError(utteranceId)
    }

    /** Speak via Android TTS. Falls back to Youdao if language data is missing. */
    private fun speakNow(tts: TextToSpeech, word: String, onFailure: ((String) -> Unit)?) {
        val langResult = tts.setLanguage(Locale.US)
        when (langResult) {
            TextToSpeech.LANG_MISSING_DATA, TextToSpeech.LANG_NOT_SUPPORTED -> {
                playYoudao(word, onFailure)
            }
            TextToSpeech.LANG_COUNTRY_AVAILABLE, TextToSpeech.LANG_AVAILABLE -> {
                Log.i(TAG, "TTS speaking: $word")
                val utteranceId = UTTERANCE_PREFIX + word
                utteranceCallbacks[utteranceId] = onFailure
                tts.speak(word, TextToSpeech.QUEUE_FLUSH, null, utteranceId)
            }
            else -> {
                playYoudao(word, onFailure)
            }
        }
    }
}

/** Creates a [TtsSpeaker] tied to this composable's lifecycle (released on dispose). */
@Composable
fun rememberTtsSpeaker(noAudio: NoAudioCache? = null): TtsSpeaker {
    val context = LocalContext.current.applicationContext
    val speaker = remember { TtsSpeaker(context, noAudio) }
    DisposableEffect(Unit) {
        onDispose { speaker.release() }
    }
    return speaker
}

/** Manual pronunciation button. Shows a toast if the pronunciation cannot be played. */
@Composable
fun AudioButton(speaker: TtsSpeaker, text: String, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    IconButton(
        onClick = { speaker.speak(text) { failed -> failToast(context, failed) } },
        modifier = modifier,
    ) {
        Icon(Icons.Default.VolumeUp, contentDescription = "播放发音")
    }
}

private fun failToast(context: Context, word: String) {
    Toast.makeText(context, "发音失败：$word", Toast.LENGTH_SHORT).show()
}

/** True when the device currently has an active network connection. */
private fun isOnline(context: Context): Boolean {
    val cm = context.getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager ?: return true
    val network = cm.activeNetwork ?: return false
    val caps = cm.getNetworkCapabilities(network) ?: return false
    return caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
}
