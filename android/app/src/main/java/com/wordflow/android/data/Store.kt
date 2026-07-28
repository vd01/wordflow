package com.wordflow.android.data

import android.content.Context
import android.content.SharedPreferences
import com.google.gson.Gson
import com.google.gson.reflect.TypeToken

/**
 * Local storage for words, review state, and config.
 * Mirrors the mini program's store.js functionality.
 */
class Store(context: Context) {
    private val prefs: SharedPreferences =
        context.getSharedPreferences("wordflow_store", Context.MODE_PRIVATE)
    private val gson = Gson()

    // ── Config ──

    var serverAddr: String
        get() = prefs.getString("serverAddr", DEFAULT_SERVER) ?: DEFAULT_SERVER
        set(value) = prefs.edit().putString("serverAddr", value).apply()

    var token: String
        get() = prefs.getString("token", "") ?: ""
        set(value) = prefs.edit().putString("token", value).apply()

    var lastSync: Long
        get() = prefs.getLong("lastSync", 0)
        set(value) = prefs.edit().putLong("lastSync", value).apply()

    var dailyLimit: Int
        get() = prefs.getInt("dailyLimit", 0)
        set(value) = prefs.edit().putInt("dailyLimit", value).apply()

    var userEmail: String
        get() = prefs.getString("userEmail", "") ?: ""
        set(value) = prefs.edit().putString("userEmail", value).apply()

    val isLoggedIn: Boolean get() = token.isNotBlank()

    // ── Words ──

    fun getWords(): Map<String, SyncEntry> {
        val json = prefs.getString("words", null) ?: return emptyMap()
        val type = object : TypeToken<Map<String, SyncEntry>>() {}.type
        return gson.fromJson(json, type) ?: emptyMap()
    }

    private fun setWords(map: Map<String, SyncEntry>) {
        prefs.edit().putString("words", gson.toJson(map)).apply()
    }

    fun getWord(id: String): SyncEntry? = getWords()[id]

    fun wordList(): List<SyncEntry> =
        getWords().values.sortedByDescending { it.createdAt }

    /** Merge pulled entries into local store (last-write-wins by updatedAt). */
    fun mergePulled(entries: List<SyncEntry>, serverNow: Long, isFullSync: Boolean): MergeResult {
        val map = getWords().toMutableMap()
        val reviews = getReviews().toMutableMap()
        var changed = 0

        val serverIds = if (isFullSync) mutableSetOf<String>() else null

        for (e in entries) {
            serverIds?.add(e.id)
            val cur = map[e.id]
            if (cur == null || e.updatedAt > cur.updatedAt) {
                if (e.deleted) {
                    map.remove(e.id)
                    reviews.remove(e.id)
                } else {
                    map[e.id] = e
                }
                changed++
            }
        }

        // Full sync: remove local entries not in server response (orphans)
        if (serverIds != null) {
            val orphans = map.keys.filter { it !in serverIds }
            for (id in orphans) {
                map.remove(id)
                reviews.remove(id)
                changed++
            }
        }

        setWords(map)
        setReviews(reviews)
        if (serverNow > 0) lastSync = serverNow

        return MergeResult(changed, map.size)
    }

    data class MergeResult(val changed: Int, val total: Int)

    // ── Review state (FSRS cards) ──

    fun getReviews(): Map<String, FsrsCard> {
        val json = prefs.getString("reviews", null) ?: return emptyMap()
        val type = object : TypeToken<Map<String, FsrsCard>>() {}.type
        return gson.fromJson(json, type) ?: emptyMap()
    }

    private fun setReviews(map: Map<String, FsrsCard>) {
        prefs.edit().putString("reviews", gson.toJson(map)).apply()
    }

    fun getReview(id: String): FsrsCard? = getReviews()[id]

    fun saveReview(id: String, card: FsrsCard) {
        val map = getReviews().toMutableMap()
        map[id] = card
        setReviews(map)
    }

    fun removeReview(id: String) {
        val map = getReviews().toMutableMap()
        map.remove(id)
        setReviews(map)
    }

    // ── Daily new card count ──

    private fun todayStr(): String {
        val d = java.util.Calendar.getInstance()
        return "%04d-%02d-%02d".format(d.get(java.util.Calendar.YEAR),
            d.get(java.util.Calendar.MONTH) + 1, d.get(java.util.Calendar.DAY_OF_MONTH))
    }

    fun getDailyCount(): DailyCount {
        val date = todayStr()
        val saved = prefs.getString("dailyCount", null)
        if (saved != null) {
            val dc = gson.fromJson(saved, DailyCount::class.java)
            if (dc?.date == date) return dc
        }
        return DailyCount(date, 0)
    }

    fun incrementDailyNewCount() {
        val c = getDailyCount()
        c.newCount++
        prefs.edit().putString("dailyCount", gson.toJson(c)).apply()
    }

    fun decrementDailyNewCount() {
        val c = getDailyCount()
        if (c.newCount > 0) c.newCount--
        prefs.edit().putString("dailyCount", gson.toJson(c)).apply()
    }

    fun isDailyNewLimitReached(): Boolean {
        val limit = dailyLimit
        if (limit <= 0) return false
        return getDailyCount().newCount >= limit
    }

    fun getDailyNewRemaining(): Int {
        val limit = dailyLimit
        if (limit <= 0) return -1 // unlimited
        return maxOf(0, limit - getDailyCount().newCount)
    }

    data class DailyCount(var date: String, var newCount: Int)

    // ── Parse result JSON ──

    @Suppress("UNUSED_ELVIS_LEFT")
    fun parseResult(entry: SyncEntry): ParsedResult? {
        if (entry.result.isBlank()) return null
        return try {
            val pr = gson.fromJson(entry.result, ParsedResult::class.java) ?: return null
            // Gson bypasses Kotlin's non-null guarantees: when the JSON value is
            // null (e.g. "tag": null produced by desktop mergeResults), reflection
            // writes null into the non-null String field. Coalesce every such field
            // back to "" so the ParsedResult contract holds and downstream
            // composables (e.g. MetaBadges) never receive null for non-null params.
            pr.copy(
                word = pr.word ?: "",
                phonetic = pr.phonetic ?: "",
                pronunciation = pr.pronunciation ?: "",
                translation = pr.translation ?: "",
                definition = pr.definition ?: "",
                pos = pr.pos ?: "",
                tag = pr.tag ?: "",
                exchange = pr.exchange ?: "",
                audioUrl = pr.audioUrl ?: "",
                memoryTips = pr.memoryTips ?: "",
                synonyms = pr.synonyms ?: "",
                antonyms = pr.antonyms ?: "",
                etymology = pr.etymology ?: "",
                definitions = pr.definitions.map { def ->
                    def.copy(
                        pos = def.pos ?: "",
                        meaning = def.meaning ?: "",
                        englishExample = def.englishExample ?: "",
                        chineseExample = def.chineseExample ?: "",
                    )
                },
            )
        } catch (e: Exception) {
            try {
                // Try manual mapping for snake_case keys
                val jsonObj = com.google.gson.JsonParser.parseString(entry.result).asJsonObject
                ParsedResult(
                    word = jsonObj.get("word")?.asString ?: "",
                    phonetic = jsonObj.get("phonetic")?.asString ?: "",
                    pronunciation = jsonObj.get("pronunciation")?.asString ?: "",
                    translation = jsonObj.get("translation")?.asString ?: "",
                    definition = jsonObj.get("definition")?.asString ?: "",
                    pos = jsonObj.get("pos")?.asString ?: "",
                    tag = jsonObj.get("tag")?.asString ?: "",
                    collins = jsonObj.get("collins")?.asInt,
                    oxford = jsonObj.get("oxford")?.asInt,
                    exchange = jsonObj.get("exchange")?.asString ?: "",
                    audioUrl = jsonObj.get("audioUrl")?.asString ?: "",
                    memoryTips = jsonObj.get("memory_tips")?.asString ?: "",
                    synonyms = jsonObj.get("synonyms")?.asString ?: "",
                    antonyms = jsonObj.get("antonyms")?.asString ?: "",
                    etymology = jsonObj.get("etymology")?.asString ?: "",
                    definitions = jsonObj.get("definitions")?.asJsonArray?.map { def ->
                        val obj = def.asJsonObject
                        DefinitionItem(
                            pos = obj.get("pos")?.asString ?: "",
                            meaning = obj.get("meaning")?.asString ?: "",
                            englishExample = obj.get("english_example")?.asString ?: "",
                            chineseExample = obj.get("chinese_example")?.asString ?: ""
                        )
                    } ?: emptyList()
                )
            } catch (e2: Exception) {
                null
            }
        }
    }

    companion object {
        const val DEFAULT_SERVER = "https://vocab-agent.duckdns.org:31588"
    }
}
