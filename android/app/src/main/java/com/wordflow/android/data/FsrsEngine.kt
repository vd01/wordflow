package com.wordflow.android.data

import java.util.Date
import kotlin.math.exp
import kotlin.math.max
import kotlin.math.min
import kotlin.math.pow
import kotlin.math.round
import kotlin.math.roundToInt

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
    val lastReview: Long = 0,    // epoch millis
    val learningSteps: Int = 0
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
 * Ported from ts-fsrs v5.4.1 (TypeScript) to match the mini program's behavior exactly.
 *
 * Default parameters from FSRS-6 (21 weights):
 *   w = [0.212, 1.2931, 2.3065, 8.2956, 6.4133, 0.8334, 3.0194, 0.001,
 *        1.8722, 0.1666, 0.796, 1.4835, 0.0614, 0.2629, 1.6483, 0.6014,
 *        1.8729, 0.5425, 0.0912, 0.0658, 0.1542]
 *   requestRetention = 0.9
 *   maximumInterval = 36500
 *   learningSteps = ["1m", "10m"]
 *   relearningSteps = ["10m"]
 */
class FsrsEngine(
    private val w: DoubleArray = doubleArrayOf(
        0.212, 1.2931, 2.3065, 8.2956, 6.4133, 0.8334, 3.0194, 0.001,
        1.8722, 0.1666, 0.796, 1.4835, 0.0614, 0.2629, 1.6483, 0.6014,
        1.8729, 0.5425, 0.0912, 0.0658, 0.1542
    ),
    private val requestRetention: Double = 0.9,
    private val maximumInterval: Int = 36500,
    private val learningSteps: List<Int> = listOf(1, 10),       // minutes
    private val relearningSteps: List<Int> = listOf(10),         // minutes
) {
    private val decay: Double = -w[20]  // FSRS-6 default: 0.1542
    private val factor: Double = (exp(1.0 / decay) * 0.9 - 1.0).let { roundTo(it, 8) }
    private val intervalModifier: Double = calculateIntervalModifier()

    // ── Public API ──

    /** Create a new FSRS card for a word entry. */
    fun createCard(id: String): FsrsCard = FsrsCard(id = id, due = System.currentTimeMillis())

    /** Apply a rating to a card, returning the updated card + log. */
    fun rateCard(card: FsrsCard, rating: Int, now: Date = Date()): RatingResult {
        val nowMs = now.time
        val elapsedDays = if (card.lastReview > 0) {
            dateDiffInDays(Date(card.lastReview), now)
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
            due = if (card.lastReview > 0) card.lastReview else card.due,
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

    /** Format an interval into a human-readable string.
     *  Handles both day-based and minute-based (learning step) intervals. */
    fun formatInterval(days: Int, card: FsrsCard? = null): String {
        // If scheduledDays is 0 and there's a due date in the near future, it's a learning step
        if (days == 0 && card != null && card.lastReview > 0) {
            val minutesLeft = ((card.due - card.lastReview) / (60.0 * 1000)).roundToInt()
            return when {
                minutesLeft < 1 -> "< 1m"
                minutesLeft < 60 -> "${minutesLeft}m"
                else -> "${(minutesLeft / 60.0).roundToInt()}h"
            }
        }
        return when {
            days < 1 -> "< 1d"
            days < 30 -> "${days}d"
            days < 365 -> "${(days / 30.0).toInt()}mo"
            else -> "${(days / 365.0).toInt()}y"
        }
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
                if (dailyNewRemaining != -1 && newAllowed <= 0) continue
                due.add(id to 0L)
                if (newAllowed > 0) newAllowed--
            } else if (isDue(card, now)) {
                due.add(id to card.due)
            }
        }

        due.sortBy { it.second }
        return due.map { it.first }
    }

    /** Get counts by state for the due queue. */
    fun getQueueCounts(
        words: Map<String, SyncEntry>,
        reviews: Map<String, FsrsCard>,
        dailyNewRemaining: Int = -1
    ): QueueCounts {
        val now = Date()
        var newCount = 0
        var learning = 0
        var review = 0
        var relearning = 0
        var newAllowed = dailyNewRemaining

        for ((id, _) in words) {
            val card = reviews[id]
            if (card == null) {
                if (dailyNewRemaining != -1 && newAllowed <= 0) continue
                newCount++
                if (newAllowed > 0) newAllowed--
            } else if (isDue(card, now)) {
                when (card.state) {
                    STATE_NEW -> {
                        if (dailyNewRemaining != -1 && newAllowed <= 0) continue
                        newCount++
                        if (newAllowed > 0) newAllowed--
                    }
                    STATE_LEARNING -> learning++
                    STATE_RELEARNING -> relearning++
                    else -> review++
                }
            }
        }

        return QueueCounts(newCount, learning, review, relearning,
            newCount + learning + review + relearning)
    }

    /** Get total counts by state for ALL words (not just due). */
    fun getTotalCounts(
        words: Map<String, SyncEntry>,
        reviews: Map<String, FsrsCard>
    ): QueueCounts {
        var newCount = 0
        var learning = 0
        var review = 0
        var relearning = 0

        for ((id, _) in words) {
            val card = reviews[id]
            if (card == null) {
                newCount++
            } else {
                when (card.state) {
                    STATE_NEW -> newCount++
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

    // ── Private: core algorithm ──

    private fun calculateIntervalModifier(): Double {
        return roundTo((requestRetention.pow(1.0 / decay) - 1.0) / factor, 8)
    }

    /** Retrievability: probability of recall after t days. */
    private fun forgettingCurve(elapsedDays: Int, stability: Double): Double {
        return roundTo((1.0 + factor * elapsedDays / stability).pow(decay), 8)
    }

    /** Date diff in whole days. */
    private fun dateDiffInDays(last: Date, cur: Date): Int {
        val diffMs = cur.time - last.time
        return (diffMs / (24.0 * 60 * 60 * 1000)).toInt().coerceAtLeast(0)
    }

    /** S_0(G) = w[G-1], clamped to [0.1, 100] */
    private fun initStability(g: Int): Double {
        return w[g - 1].coerceIn(0.1, 100.0)
    }

    /** D_0(G) = w[4] - exp((G-1) * w[5]) + 1, clamped to [1, 10] */
    private fun initDifficulty(g: Int): Double {
        return roundTo(w[4] - exp((g - 1.0) * w[5]) + 1.0, 8).coerceIn(1.0, 10.0)
    }

    /** Linear damping: delta_d * (10 - old_d) / 9 */
    private fun linearDamping(deltaD: Double, oldD: Double): Double {
        return roundTo(deltaD * (10.0 - oldD) / 9.0, 8)
    }

    /** Mean reversion: w[7] * init + (1 - w[7]) * current */
    private fun meanReversion(init: Double, current: Double): Double {
        return roundTo(w[7] * init + (1.0 - w[7]) * current, 8)
    }

    /** Next difficulty after a rating. */
    private fun nextDifficulty(d: Double, g: Int): Double {
        val deltaD = -w[6] * (g - 3.0)
        val nextD = d + linearDamping(deltaD, d)
        return meanReversion(initDifficulty(RATING_EASY), nextD).coerceIn(1.0, 10.0)
    }

    /** Next stability after recall (Hard/Good/Easy). */
    private fun nextRecallStability(d: Double, s: Double, r: Double, g: Int): Double {
        val hardPenalty = if (g == RATING_HARD) w[15] else 1.0
        val easyBonus = if (g == RATING_EASY) w[16] else 1.0
        val newS = s * (1.0 + exp(w[8]) * (11.0 - d) * s.pow(-w[9]) *
                (exp((1.0 - r) * w[10]) - 1.0) * hardPenalty * easyBonus)
        return roundTo(newS.coerceIn(0.001, 36500.0), 8)
    }

    /** Next stability after forgetting (Again). */
    private fun nextForgetStability(d: Double, s: Double, r: Double): Double {
        val newS = w[11] * d.pow(-w[12]) * ((s + 1.0).pow(w[13]) - 1.0) *
                exp((1.0 - r) * w[14])
        return roundTo(newS.coerceIn(0.001, 36500.0), 8)
    }

    /** Next short-term stability (t=0, FSRS-6). */
    private fun nextShortTermStability(s: Double, g: Int): Double {
        val sinc = s.pow(-w[19]) * exp(w[17] * (g - 3.0 + w[18]))
        val maskedSinc = if (g >= RATING_HARD) max(sinc, 1.0) else sinc
        return roundTo((s * maskedSinc).coerceIn(0.001, 36500.0), 8)
    }

    /** Calculate next memory state (difficulty + stability). */
    private fun nextState(
        d: Double, s: Double, t: Int, g: Int, r: Double? = null
    ): Pair<Double, Double> {
        if (d == 0.0 && s == 0.0) {
            return initDifficulty(g).coerceIn(1.0, 10.0) to initStability(g)
        }
        val retrievability = r ?: forgettingCurve(t, s)
        val newS: Double
        if (t == 0) {
            // Short-term review
            newS = nextShortTermStability(s, g)
        } else if (g == RATING_AGAIN) {
            val sAfterFail = nextForgetStability(d, s, retrievability)
            val nextSMin = s / exp(w[17] * w[18])
            newS = roundTo(nextSMin, 8).coerceIn(0.001, sAfterFail)
        } else {
            newS = nextRecallStability(d, s, retrievability, g)
        }
        val newD = nextDifficulty(d, g)
        return newD to newS
    }

    /** Next interval in days from stability. */
    private fun nextInterval(s: Double, elapsedDays: Int = 0): Int {
        val newInterval = (s * intervalModifier).roundToInt().coerceIn(1, maximumInterval)
        return newInterval
    }

    /** Schedule a due date from nowMs + days. */
    private fun scheduleDueDays(nowMs: Long, days: Int): Long {
        return nowMs + days * 24L * 60 * 60 * 1000
    }

    /** Schedule a due date from nowMs + minutes. */
    private fun scheduleDueMinutes(nowMs: Long, minutes: Int): Long {
        return nowMs + minutes * 60L * 1000
    }

    /** Round to N decimal places. */
    private fun roundTo(v: Double, decimals: Int): Double {
        val f = 10.0.pow(decimals)
        return round(v * f) / f
    }

    // ── Private: learning steps ──

    data class StepInfo(val scheduledMinutes: Int, val nextStep: Int)

    /** Get the learning step info for a given grade and current step. */
    private fun getLearningStepInfo(
        state: Int, curStep: Int, grade: Int
    ): StepInfo? {
        val steps = if (state == STATE_RELEARNING || state == STATE_REVIEW) relearningSteps else learningSteps
        if (steps.isEmpty() || curStep >= steps.size) return null

        val firstStep = steps[0]

        when (grade) {
            RATING_AGAIN -> {
                return StepInfo(firstStep, 0)
            }
            RATING_HARD -> {
                val hardMin = if (steps.size == 1) {
                    (firstStep * 1.5).roundToInt()
                } else {
                    ((firstStep + steps[1]) / 2.0).roundToInt()
                }
                return StepInfo(hardMin, curStep)
            }
            RATING_GOOD -> {
                val nextStepIdx = curStep + 1
                if (nextStepIdx < steps.size) {
                    return StepInfo(steps[nextStepIdx], nextStepIdx)
                }
                // No more steps — will graduate
                return null
            }
        }
        return null
    }

    /** Apply learning steps to a card. Returns the card with due/state set. */
    private fun applyLearningSteps(
        card: FsrsCard, grade: Int, toState: Int, nowMs: Long
    ): FsrsCard {
        val stepInfo = getLearningStepInfo(card.state, card.learningSteps, grade)

        if (stepInfo != null && stepInfo.scheduledMinutes > 0 && stepInfo.scheduledMinutes < 1440) {
            // Short-term step (minutes): stay in Learning/Relearning
            return card.copy(
                learningSteps = stepInfo.nextStep,
                scheduledDays = 0,
                state = toState,
                due = scheduleDueMinutes(nowMs, stepInfo.scheduledMinutes),
                lastReview = nowMs
            )
        } else if (stepInfo != null && stepInfo.scheduledMinutes >= 1440) {
            // Long step (≥1 day): stay in Learning/Relearning but schedule as days
            return card.copy(
                learningSteps = stepInfo.nextStep,
                scheduledDays = stepInfo.scheduledMinutes / 1440,
                state = toState,
                due = scheduleDueMinutes(nowMs, stepInfo.scheduledMinutes),
                lastReview = nowMs
            )
        } else {
            // No step info or graduated: graduate to Review
            val interval = nextInterval(card.stability, 0)
            return card.copy(
                learningSteps = 0,
                scheduledDays = interval,
                state = STATE_REVIEW,
                due = scheduleDueDays(nowMs, interval),
                lastReview = nowMs
            )
        }
    }

    // ── Private: rating logic ──

    private fun rateNew(card: FsrsCard, rating: Int, elapsedDays: Int, nowMs: Long): FsrsCard {
        val (newD, newS) = nextState(0.0, 0.0, 0, rating)
        var next = card.copy(
            stability = newS,
            difficulty = newD,
            elapsedDays = elapsedDays,
            reps = card.reps + 1,
            lapses = if (rating == RATING_AGAIN) card.lapses + 1 else card.lapses,
            lastReview = nowMs
        )
        // Apply learning steps (for Again/Hard/Good) or direct to Review (for Easy)
        next = applyLearningSteps(next, rating, STATE_LEARNING, nowMs)
        return next
    }

    private fun rateLearning(card: FsrsCard, rating: Int, elapsedDays: Int, nowMs: Long): FsrsCard {
        // Use the card's PREVIOUS state (Learning or Relearning) to preserve it
        val toState = card.state

        val (newD, newS) = nextState(card.difficulty, card.stability, 0, rating)
        var next = card.copy(
            stability = newS,
            difficulty = newD,
            elapsedDays = elapsedDays,
            reps = card.reps + 1,
            lapses = if (rating == RATING_AGAIN) card.lapses + 1 else card.lapses,
            lastReview = nowMs
        )
        next = applyLearningSteps(next, rating, toState, nowMs)
        return next
    }

    private fun rateReview(card: FsrsCard, rating: Int, elapsedDays: Int, nowMs: Long): FsrsCard {
        val retrievability = forgettingCurve(elapsedDays, card.stability)
        var nextLapses = card.lapses

        if (rating == RATING_AGAIN) {
            val (newD, newS) = nextState(card.difficulty, card.stability, elapsedDays, rating, retrievability)
            nextLapses++
            val next = card.copy(
                stability = newS,
                difficulty = newD,
                elapsedDays = elapsedDays,
                reps = card.reps + 1,
                lapses = nextLapses,
                lastReview = nowMs
            )
            // Apply relearning steps for Again
            val result = applyLearningSteps(next, RATING_AGAIN, STATE_RELEARNING, nowMs)
            return result
        } else {
            val (newD, newS) = nextState(card.difficulty, card.stability, elapsedDays, rating, retrievability)
            val interval = nextInterval(newS, elapsedDays)
            return card.copy(
                due = scheduleDueDays(nowMs, interval),
                stability = newS,
                difficulty = newD,
                elapsedDays = elapsedDays,
                scheduledDays = interval,
                reps = card.reps + 1,
                lapses = card.lapses,
                state = STATE_REVIEW,
                learningSteps = 0,
                lastReview = nowMs
            )
        }
    }
}
