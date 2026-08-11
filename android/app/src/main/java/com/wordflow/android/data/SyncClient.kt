package com.wordflow.android.data

import android.content.Context
import com.google.gson.Gson
import com.google.gson.JsonParser
import com.google.net.cronet.okhttptransport.CronetInterceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.chromium.net.CronetEngine
import java.io.IOException
import java.util.concurrent.TimeUnit

/**
 * HTTP client for the WordFlow SyncServer API.
 * Uses Cronet (HTTP/3 / QUIC) for faster connections with automatic fallback.
 */
class SyncClient(context: Context) {
    private val client: OkHttpClient

    init {
        val cronetEngine = CronetEngine.Builder(context)
            .enableQuic(true)
            .build()

        client = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .writeTimeout(30, TimeUnit.SECONDS)
            // CronetInterceptor must be added LAST — it redirects requests
            // to Cronet (QUIC/HTTP3) when available, falls back to OkHttp otherwise.
            .addInterceptor(CronetInterceptor.newBuilder(cronetEngine).build())
            .build()
    }

    private val gson = Gson()
    private val jsonType = "application/json; charset=utf-8".toMediaType()

    // ── Public API ──

    fun health(serverAddr: String): HealthResponse {
        val url = base(serverAddr) + "/api/v1/health"
        return get(url, HealthResponse::class.java)
    }

    fun getStatus(serverAddr: String, token: String): UserStatusResponse {
        val url = base(serverAddr) + "/api/v1/user/status"
        return get(url, UserStatusResponse::class.java, token)
    }

    fun pull(serverAddr: String, token: String, since: Long): SyncPullResponse {
        val q = if (since > 0) "?since=$since" else ""
        val url = base(serverAddr) + "/api/v1/sync/pull$q"
        return get(url, SyncPullResponse::class.java, token)
    }

    fun push(serverAddr: String, token: String, entries: List<SyncEntry>): Int {
        val url = base(serverAddr) + "/api/v1/sync/push"
        val body = gson.toJson(SyncPushRequest(entries))
        val json = post(url, body, token)
        val obj = JsonParser.parseString(json).asJsonObject
        return obj.get("upserted")?.asInt ?: 0
    }

    // ── Review state sync ──

    /** Push review cards to server. Returns number of cards upserted. */
    fun pushReviews(serverAddr: String, token: String, cards: List<FsrsCard>): Int {
        val url = base(serverAddr) + "/api/v1/sync/reviews/push"
        val body = gson.toJson(mapOf("cards" to cards))
        val json = post(url, body, token)
        val obj = JsonParser.parseString(json).asJsonObject
        return obj.get("upserted")?.asInt ?: 0
    }

    /** Pull review cards from server. */
    fun pullReviews(serverAddr: String, token: String, since: Long): ReviewPullResponse {
        val q = if (since > 0) "?since=$since" else ""
        val url = base(serverAddr) + "/api/v1/sync/reviews/pull$q"
        return get(url, ReviewPullResponse::class.java, token)
    }

    // ── Email Auth ──

    /** Request a verification code be sent to the given email. */
    fun requestEmailCode(serverAddr: String, email: String): String {
        val url = base(serverAddr) + "/api/v1/auth/email/request"
        val body = gson.toJson(EmailAuthRequest(email))
        val json = post(url, body)
        val obj = JsonParser.parseString(json).asJsonObject
        return obj.get("message")?.asString ?: "Code sent"
    }

    /** Verify the email code and get a token. */
    fun verifyEmailCode(serverAddr: String, email: String, code: String): AuthResponse {
        val url = base(serverAddr) + "/api/v1/auth/email/verify"
        val body = gson.toJson(EmailLoginRequest(email, code))
        return post(url, body, AuthResponse::class.java)
    }

    /** Verify a pairing code and get a token. No email required. */
    fun verifyPairCode(serverAddr: String, code: String): AuthResponse {
        val url = base(serverAddr) + "/api/v1/auth/pair/verify"
        val body = gson.toJson(PairCodeRequest(code))
        return post(url, body, AuthResponse::class.java)
    }

    // ── Internal ──

    private fun base(addr: String): String =
        addr.trim().trimEnd('/')

    private fun authHeader(token: String) = mapOf(
        "Authorization" to "Bearer $token",
        "Content-Type" to "application/json"
    )

    @Throws(IOException::class)
    private fun <T> get(url: String, clazz: Class<T>, token: String? = null): T {
        val req = Request.Builder().url(url).get()
        if (token != null) {
            req.header("Authorization", "Bearer $token")
        }
        val resp = try {
            client.newCall(req.build()).execute()
        } catch (e: IOException) {
            throw IOException("Connection failed: ${e.message ?: e.javaClass.simpleName}")
        }
        val body = resp.body?.string() ?: throw IOException("Empty response")
        if (!resp.isSuccessful) {
            if (resp.code == 401) throw AuthException("Token invalid or expired, please re-login")
            if (resp.code == 429) throw RateLimitException("Too many requests, please try again later")
            val msg = try {
                JsonParser.parseString(body).asJsonObject.get("error")?.asString ?: "HTTP ${resp.code}"
            } catch (_: Exception) { "HTTP ${resp.code}" }
            throw IOException(msg)
        }
        return gson.fromJson(body, clazz)
    }

    @Throws(IOException::class)
    private fun post(url: String, body: String, token: String? = null): String {
        val req = Request.Builder().url(url)
            .post(body.toRequestBody(jsonType))
        if (token != null) {
            req.header("Authorization", "Bearer $token")
        }
        val resp = try {
            client.newCall(req.build()).execute()
        } catch (e: IOException) {
            throw IOException("Connection failed: ${e.message ?: e.javaClass.simpleName}")
        }
        val respBody = resp.body?.string() ?: throw IOException("Empty response")
        if (!resp.isSuccessful) {
            if (resp.code == 401) throw AuthException("Token invalid or expired")
            val msg = try {
                JsonParser.parseString(respBody).asJsonObject.get("error")?.asString ?: "HTTP ${resp.code}"
            } catch (_: Exception) { "HTTP ${resp.code}" }
            throw IOException(msg)
        }
        return respBody
    }

    @Throws(IOException::class)
    private fun <T> post(url: String, body: String, clazz: Class<T>, token: String? = null): T {
        val json = post(url, body, token)
        return gson.fromJson(json, clazz)
    }

    class AuthException(message: String) : IOException(message)
    class RateLimitException(message: String) : IOException(message)
}
