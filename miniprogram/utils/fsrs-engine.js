// FSRS engine wrapper for WordWise Mini Program.
// Vendors ts-fsrs (CJS bundle) and provides a simple API:
//   - createCard(id) → new FSRS card for a word
//   - rateCard(card, rating) → updated { card, log }
//   - getDueQueue() → sorted list of word IDs due for review today
//   - previewIntervals(card) → { again, hard, good, easy } next intervals

const tsFsrs = require('./ts-fsrs')

// ts-fsrs CJS exports: { fsrs, Rating, createEmptyCard, generator, ... }
const { fsrs, Rating, createEmptyCard } = tsFsrs

const scheduler = fsrs() // default params (FSRS-6)

// Rating constants (exposed for UI)
const RATING_AGAIN = Rating.Again  // 1
const RATING_HARD  = Rating.Hard   // 2
const RATING_GOOD  = Rating.Good   // 3
const RATING_EASY  = Rating.Easy   // 4

// Card states (for display)
const STATE_NEW       = 0
const STATE_LEARNING  = 1
const STATE_REVIEW    = 2
const STATE_RELEARNING = 3

const STATE_LABELS = {
  [STATE_NEW]: 'New',
  [STATE_LEARNING]: 'Learning',
  [STATE_REVIEW]: 'Review',
  [STATE_RELEARNING]: 'Relearning'
}

/**
 * Create a new FSRS card for a word entry.
 * @param {string} id - SyncEntry ID
 * @returns {object} FSRS card with id attached
 */
function createCard(id) {
  const card = createEmptyCard()
  card.id = id
  return card
}

/**
 * Apply a rating to a card, returning the updated card + log.
 * @param {object} card - current FSRS card
 * @param {number} rating - Rating.Again/Hard/Good/Easy
 * @param {Date} [now] - review timestamp (defaults to now)
 * @returns {{ card: object, log: object }}
 */
function rateCard(card, rating, now) {
  now = now || new Date()
  return scheduler.next(card, now, rating)
}

/**
 * Preview the next intervals for all 4 ratings (for button labels).
 * @param {object} card - current FSRS card
 * @param {Date} [now]
 * @returns {{ again: object, hard: object, good: object, easy: object }}
 *   Each has .card with .scheduled_days, .due, etc.
 */
function previewIntervals(card, now) {
  now = now || new Date()
  const repeat = scheduler.repeat(card, now)
  return {
    again: repeat[RATING_AGAIN],
    hard:  repeat[RATING_HARD],
    good:  repeat[RATING_GOOD],
    easy:  repeat[RATING_EASY]
  }
}

/**
 * Format a scheduled_days value into a human-readable interval string.
 * @param {number} days
 * @returns {string}
 */
function formatInterval(days) {
  if (days < 1) return '< 1d'
  if (days < 30) return Math.round(days) + 'd'
  if (days < 365) return Math.round(days / 30) + 'mo'
  return Math.round(days / 365) + 'y'
}

/**
 * Check if a card is due for review now.
 * @param {object} card - FSRS card
 * @param {Date} [now]
 * @returns {boolean}
 */
function isDue(card, now) {
  now = now || new Date()
  if (card.state === STATE_NEW) return true
  return new Date(card.due) <= now
}

/**
 * Build the due queue: list of word IDs that need review, sorted by due date.
 * @param {object} words - { [id]: SyncEntry } from store
 * @param {object} reviews - { [id]: FsrsCard } from store
 * @returns {string[]} sorted word IDs due now
 */
function getDueQueue(words, reviews) {
  const now = new Date()
  const ids = Object.keys(words)
  const due = []

  for (const id of ids) {
    const card = reviews[id]
    if (!card) {
      // New word — no review state yet → due
      due.push({ id, dueDate: 0 })
    } else if (isDue(card, now)) {
      due.push({ id, dueDate: new Date(card.due).getTime() })
    }
  }

  // Sort: earliest due first (new words at front)
  due.sort((a, b) => a.dueDate - b.dueDate)
  return due.map((d) => d.id)
}

/**
 * Get counts by state for session summary.
 * @param {object} words
 * @param {object} reviews
 * @returns {{ new: number, learning: number, review: number, relearning: number, total: number }}
 */
function getQueueCounts(words, reviews) {
  const now = new Date()
  let newCount = 0, learning = 0, review = 0, relearning = 0

  for (const id of Object.keys(words)) {
    const card = reviews[id]
    if (!card) {
      newCount++
    } else if (isDue(card, now)) {
      if (card.state === STATE_LEARNING) learning++
      else if (card.state === STATE_RELEARNING) relearning++
      else review++
    }
  }

  return {
    new: newCount,
    learning,
    review,
    relearning,
    total: newCount + learning + review + relearning
  }
}

module.exports = {
  createCard,
  rateCard,
  previewIntervals,
  formatInterval,
  isDue,
  getDueQueue,
  getQueueCounts,
  RATING_AGAIN,
  RATING_HARD,
  RATING_GOOD,
  RATING_EASY,
  STATE_NEW,
  STATE_LEARNING,
  STATE_REVIEW,
  STATE_RELEARNING,
  STATE_LABELS
}
