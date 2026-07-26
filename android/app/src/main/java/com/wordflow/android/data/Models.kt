package com.wordflow.android.data

import com.google.gson.annotations.SerializedName

/** A synced word entry from the SyncServer. */
data class SyncEntry(
    val id: String = "",
    val word: String = "",
    val result: String = "",       // JSON string of merged ECDICT+LLM result
    val createdAt: Long = 0,
    val updatedAt: Long = 0,
    val deleted: Boolean = false
)

/** Pull response from the server. */
data class SyncPullResponse(
    val entries: List<SyncEntry> = emptyList(),
    val serverNow: Long = 0
)

/** Push request body. */
data class SyncPushRequest(
    val entries: List<SyncEntry> = emptyList()
)

/** Health check response. */
data class HealthResponse(
    val status: String = "",
    val service: String = "",
    val version: String = "",
    val time: Long = 0,
    val wechat: Boolean = false,
    val email: Boolean = false
)

/** User status response. */
data class UserStatusResponse(
    val token: String = "",
    val wordCount: Int = 0,
    val lastSync: Long = 0,
    val createdAt: Long = 0
)

/** Email auth code request. */
data class EmailAuthRequest(
    val email: String = ""
)

/** Email login request (code verification). */
data class EmailLoginRequest(
    val email: String = "",
    val code: String = ""
)

/** Generic auth response. */
data class AuthResponse(
    val token: String = "",
    val message: String = ""
)

/** Parsed result from a SyncEntry's result JSON. */
data class ParsedResult(
    val word: String = "",
    val phonetic: String = "",
    val pronunciation: String = "",
    val translation: String = "",
    val definition: String = "",
    val pos: String = "",
    val tag: String = "",
    val collins: Int? = null,
    val oxford: Int? = null,
    val exchange: String = "",
    val audioUrl: String = "",
    val memoryTips: String = "",
    val synonyms: String = "",
    val antonyms: String = "",
    val etymology: String = "",
    val definitions: List<DefinitionItem> = emptyList()
) {
    val hasContent: Boolean
        get() = translation.isNotBlank() || definition.isNotBlank() ||
                definitions.isNotEmpty() || memoryTips.isNotBlank() ||
                synonyms.isNotBlank() || antonyms.isNotBlank() ||
                etymology.isNotBlank() || exchange.isNotBlank() || pos.isNotBlank() || tag.isNotBlank()
}

data class DefinitionItem(
    val pos: String = "",
    val meaning: String = "",
    val englishExample: String = "",
    val chineseExample: String = ""
)
