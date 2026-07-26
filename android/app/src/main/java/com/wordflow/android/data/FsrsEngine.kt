package com.wordflow.android.data

import java.util.Date
import kotlin.math.max
import kotlin.math.min
import kotlin.math.pow

// ── Rating constants ──
const val RATING_AGAIN = 1
const val RATING_HARD = 2
const val RATING_GOOD = 3
const val RATING_EASY = 4

// ── Card state constants ──
const val STATE_NEW = 0
const val STATE_LEARNING = 1
const val STATE_REVIEW = 2
const val STATE_RELEARNING = 3

val STATE_LABELS = mapOf(
    STATE_NEW to "New",
    STATE_LEARNING to "Learning",
    STATE_REVIEW to "Review",
    STATE_RELEARNING to "Relearning"
)

/**
 * FSRS card state, stored per word.
 * Mirrors the ts-fsrs Card type.
 */
data class FsrsCard(
    val id: String = "",
    val due: Long = 0,           // epoch millis
    val stability: Double = 0.0,
    val difficulty: Double = 0.0,
    val elapsedDays: Int = 0,
    val scheduledDays: Int = 0,
    val reps: Int = 0,
    val lapses: Int = 0,
    val state: Int = STATE_NEW,
    val lastReview: Long = 0     // epoch millis
)

/**
 * Result of rating a card.
 */
data class RatingResult(
    val card: FsrsCard,
    val log: FsrsLog
)

data class FsrsLog(
    val rating: Int = 0,
    val state: Int = 0,
    val due: Long = 0,
    val stability: Double = 0.0,
    val difficulty: Double = 0.0,
    val elapsedDays: Int = 0,
    val lastElapsedDays: Int = 0,
    val scheduledDays: Int = 0,
    val review: Long = 0
)

/**
 * Preview of intervals for all 4 ratings.
 */
data class IntervalPreview(
    val again: FsrsCard,
    val hard: FsrsCard,
    val good: FsrsCard,
    val easy: FsrsCard
)

/**
 * FSRS-6 algorithm implementation in Kotlin.
 * Ported from ts-fsrs (TypeScript) to match the mini program's behavior exactly.
 *
 * Default parameters from FSRS-6:
 *   w = [0.4, 0.6, 2.4, 5.8, 4.93, 0.94, 0.86, 0.01, 1.49, 0.14, 0.94, 2.18, 0.05, 0.34, 1.26, 0.29, 2.61]
 *   requestRetention = 0.9
 *   maximumInterval = 36500
 */
class FsrsEngine(
    private val w: DoubleArray = doubleArrayOf(
        0.4, 0.6, 2.4, 5.8, 4.93, 0.94, 0.86, 0.01, 1.49, 0.14, 0.94, 2.18, 0.05, 0.34, 1.26, 0.29, 2.61
    ),
    private val requestRetention: Double = 0.9,
    private val maximumInterval: Int = 36500,
    private val decay: Double = -0.5,
    private val factor: Double = 19.0 / 81.0
) {
    private val hardIntervalFactor = 1.2 // w[15] is not used for hard interval in FSRS-6 simplified

    /** Create a new FSRS card for a word entry. */
    fun createCard(id: String): FsrsCard = FsrsCard(id = id)

    /** Apply a rating to a card, returning the updated card + log. */
    fun rateCard(card: FsrsCard, rating: Int, now: Date = Date()): RatingResult {
        val nowMs = now.time
        val elapsedDays = if (card.lastReview > 0) {
            ((nowMs - card.lastReview) / (1000.0 * 60 * 60 * 24)).toInt().coerceAtLeast(0)
        } else 0

        val newCard = when (card.state) {
            STATE_NEW -> rateNew(card, rating, elapsedDays, nowMs)
            STATE_LEARNING, STATE_RELEARNING -> rateLearning(card, rating, elapsedDays, nowMs)
            STATE_REVIEW -> rateReview(card, rating, elapsedDays, nowMs)
            else -> card
        }

        val log = FsrsLog(
            rating = rating,
            state = card.state,
            due = card.due,
            stability = card.stability,
            difficulty = card.difficulty,
            elapsedDays = elapsedDays,
            lastElapsedDays = card.elapsedDays,
            scheduledDays = newCard.scheduledDays,
            review = nowMs
        )

        return RatingResult(newCard, log)
    }

    /** Preview the next intervals for all 4 ratings. */
    fun previewIntervals(card: FsrsCard, now: Date = Date()): IntervalPreview {
        return IntervalPreview(
            again = rateCard(card, RATING_AGAIN, now).card,
            hard = rateCard(card, RATING_HARD, now).card,
            good = rateCard(card, RATING_GOOD, now).card,
            easy = rateCard(card, RATING_EASY, now).card
        )
    }

    /** Format a scheduled_days value into a human-readable interval string. */
    fun formatInterval(days: Int): String = when {
        days < 1 -> "< 1d"
        days < 30 -> "${days}d"
        days < 365 -> "${(days / 30.0).toInt()}mo"
        else -> "${(days / 365.0).toInt()}y"
    }

    /** Check if a card is due for review now. */
    fun isDue(card: FsrsCard, now: Date = Date()): Boolean {
        if (card.state == STATE_NEW) return true
        return card.due <= now.time
    }

    /** Build the due queue: list of word IDs that need review, sorted by due date. */
    fun getDueQueue(
        words: Map<String, SyncEntry>,
        reviews: Map<String, FsrsCard>,
        dailyNewRemaining: Int = -1
    ): List<String> {
        val now = Date()
        val due = mutableListOf<Pair<String, Long>>()
        var newAllowed = dailyNewRemaining

        for ((id, _) in words) {
            val card = reviews[id]
            if (card == null) {
                // New word — no review state yet
                if (dailyNewRemaining != -1 && newAllowed <= 0) continue
                due.add(id to 0L)
                if (newAllowed > 0) newAllowed--
            } else if (isDue(card, now)) {
                due.add(id to card.due)
            }
        }

        // Sort: earliest due first (new words at front)
        due.sortBy { it.second }
        return due.map { it.first }
    }

    /** Get counts by state for session summary. */
    fun getQueueCounts(
        words: Map<String, SyncEntry>,
        reviews: Map<String, FsrsCard>
    ): QueueCounts {
        val now = Date()
        var newCount = 0
        var learning = 0
        var review = 0
        var relearning = 0

        for ((id, _) in words) {
            val card = reviews[id]
            if (card == null) {
                newCount++
            } else if (isDue(card, now)) {
                when (card.state) {
                    STATE_LEARNING -> learning++
                    STATE_RELEARNING -> relearning++
                    else -> review++
                }
            }
        }

        return QueueCounts(newCount, learning, review, relearning,
            newCount + learning + review + relearning)
    }

    data class QueueCounts(
        val new: Int, val learning: Int, val review: Int, val relearning: Int, val total: Int
    )

    // ── Private: rating logic ──

    private fun rateNew(card: FsrsCard, rating: Int, elapsedDays: Int, nowMs: Long): FsrsCard {
        val nextStability = initStability(rating)
        val nextDifficulty = initDifficulty(rating)
        val nextScheduledDays = nextInterval(nextStability).toInt().coerceAtLeast(1)
        val nextState = if (rating == RATING_AGAIN) STATE_LEARNING else STATE_REVIEW
        val nextDue = nowMs + nextScheduledDays * 24L * 60 * 60 * 1000

        return card.copy(
            due = nextDue,
            stability = nextStability,
            difficulty = nextDifficulty,
            elapsedDays = elapsedDays,
            scheduledDays = nextScheduledDays,
            reps = card.reps + 1,
            lapses = if (rating == RATING_AGAIN) card.lapses + 1 else card.lapses,
            state = nextState,
            lastReview = nowMs
        )
    }

    private fun rateLearning(card: FsrsCard, rating: Int, elapsedDays: Int, nowMs: Long): FsrsCard {
        var nextStability = card.stability
        var nextDifficulty = card.difficulty
        var nextScheduledDays: Int
        var nextState: Int

        if (rating == RATING_AGAIN) {
            nextStability = nextStabilityAgain(card.stability)
            nextScheduledDays = nextInterval(nextStability).toInt().coerceAtLeast(1)
            nextState = STATE_LEARNING
        } else {
            nextStability = nextStabilityGood(card.stability, card.difficulty, rating)
            nextDifficulty = nextDifficulty(card.difficulty, rating)
            nextScheduledDays = nextInterval(nextStability).toInt().coerceAtLeast(1)
            nextState = STATE_REVIEW
        }

        val nextDue = nowMs + nextScheduledDays * 24L * 60 * 60 * 1000

        return card.copy(
            due = nextDue,
            stability = nextStability,
            difficulty = nextDifficulty,
            elapsedDays = elapsedDays,
            scheduledDays = nextScheduledDays,
            reps = card.reps + 1,
            lapses = if (rating == RATING_AGAIN) card.lapses + 1 else card.lapses,
            state = nextState,
            lastReview = nowMs
        )
    }

    private fun rateReview(card: FsrsCard, rating: Int, elapsedDays: Int, nowMs: Long): FsrsCard {
        var nextStability: Double
        var nextDifficulty = nextDifficulty(card.difficulty, rating)
        var nextScheduledDays: Int
        var nextState: Int
        var nextLapses = card.lapses

        if (rating == RATING_AGAIN) {
            nextStability = nextStabilityAgain(card.stability)
            nextScheduledDays = nextInterval(nextStability).toInt().coerceAtLeast(1)
            nextState = STATE_RELEARNING
            nextLapses++
        } else {
            nextStability = nextStabilityGood(card.stability, card.difficulty, rating)
            nextScheduledDays = nextInterval(nextStability).toInt().coerceAtLeast(1)
            nextScheduledDays = min(nextScheduledDays, maximumInterval)
            nextState = STATE_REVIEW
        }

        val nextDue = nowMs + nextScheduledDays * 24L * 60 * 60 * 1000

        return card.copy(
            due = nextDue,
            stability = nextStability,
            difficulty = nextDifficulty,
            elapsedDays = elapsedDays,
            scheduledDays = nextScheduledDays,
            reps = card.reps + 1,
            lapses = nextLapses,
            state = nextState,
            lastReview = nowMs
        )
    }

    // ── FSRS-6 core formulas ──

    private fun initStability(rating: Int): Double = w[rating - 1]

    private fun initDifficulty(rating: Int): Double {
        return (w[4] - (w[5] * (rating - 3))).coerceIn(1.0, 10.0)
    }

    private fun nextDifficulty(d: Double, rating: Int): Double {
        val delta = w[6] * (initDifficulty(rating) - d)
        val newD = (d + delta).coerceIn(1.0, 10.0)
        // Mean revert
        return w[7] * initDifficulty(RATING_GOOD) + (1 - w[7]) * newD
    }

    private fun nextStabilityAgain(s: Double): Double {
        return (w[11] * s.pow(w[12]) * (s.pow(w[13]) * Math.E).pow((1 - requestRetention) * w[14]) - w[15] * (1 - requestRetention) * w[16])
            .coerceAtLeast(0.1)
    }

    private fun nextStabilityGood(s: Double, d: Double, rating: Int): Double {
        val hardPenalty = if (rating == RATING_HARD) w[15] else 1.0
        val easyBonus = if (rating == RATING_EASY) w[16] else 1.0
        val newS = s * (1 + Math.E.pow(w[8]) * (11 - d) * s.pow(-w[9]) *
                (Math.E.pow(w[10] * (1 - requestRetention)) - 1) * hardPenalty * easyBonus)
        return newS.coerceAtLeast(0.1)
    }

    private fun nextInterval(s: Double): Double {
        return (9.0 * s * (1.0 / requestRetention - 1.0)).coerceAtLeast(1.0)
    }
}
