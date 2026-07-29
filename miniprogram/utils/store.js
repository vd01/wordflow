// Local storage of words pulled from SyncServer + FSRS review state.
// Review state is synced to the server so it persists across devices/reinstalls.

const WORDS_KEY = 'wordwise_words'     // { [id]: SyncEntry }
const REVIEWS_KEY = 'wordwise_reviews' // { [id]: FsrsCard }
const LASTSYNC_KEY = 'wordwise_lastsync' // last successful pull (serverNow, unix seconds)
const DAILY_LIMIT_KEY = 'wordwise_daily_limit' // max new cards per day (0 = unlimited)
const DAILY_COUNT_KEY = 'wordwise_daily_count' // { date: 'YYYY-MM-DD', newCount: N }

function getWords() {
  try { return wx.getStorageSync(WORDS_KEY) || {} } catch (e) { return {} }
}
function setWords(map) {
  try { wx.setStorageSync(WORDS_KEY, map) } catch (e) { handleStorageError(e) }
}
function getLastSync() {
  try { return wx.getStorageSync(LASTSYNC_KEY) || 0 } catch (e) { return 0 }
}
function setLastSync(ts) {
  try { wx.setStorageSync(LASTSYNC_KEY, ts) } catch (e) { handleStorageError(e) }
}

// --- Review state (FSRS cards) ---

function getReviews() {
  try { return wx.getStorageSync(REVIEWS_KEY) || {} } catch (e) { return {} }
}
function setReviews(map) {
  try { wx.setStorageSync(REVIEWS_KEY, map) } catch (e) { handleStorageError(e) }
}

/** Get a single review card by entry ID */
function getReview(id) {
  const map = getReviews()
  return map[id] || null
}

/** Save/update a single review card */
function saveReview(id, card) {
  const map = getReviews()
  map[id] = card
  setReviews(map)
}

/** Remove review state for a word (e.g. when word is deleted) */
function removeReview(id) {
  const map = getReviews()
  delete map[id]
  setReviews(map)
}

/** Merge pulled review cards into local store. Last-write-wins by lastReview. */
function mergePulledReviews(cards) {
  const local = getReviews()
  let changed = 0
  ;(cards || []).forEach((card) => {
    if (!card || !card.id) return
    const cur = local[card.id]
    if (!cur || (card.lastReview || 0) > (cur.lastReview || 0)) {
      local[card.id] = card
      changed++
    }
  })
  if (changed > 0) setReviews(local)
  return changed
}

/** Get all review cards as an array (for pushing to server). */
function getAllReviewCards() {
  const map = getReviews()
  return Object.keys(map).map((id) => map[id])
}

// --- Word operations ---

// Merge pulled entries into local store (last-write-wins by updatedAt; soft-deletes remove).
// When isFullSync is true (initial/full pull), local entries not present in the server
// response are removed — the server response is treated as the source of truth.
function mergePulled(entries, serverNow, isFullSync) {
  const map = getWords()
  const reviews = getReviews()
  let changed = 0

  // Build a set of server entry IDs for full-sync reconciliation
  const serverIds = isFullSync ? new Set() : null

  ;(entries || []).forEach((e) => {
    if (serverIds) serverIds.add(e.id)
    const cur = map[e.id]
    if (!cur || (e.updatedAt || 0) > (cur.updatedAt || 0)) {
      if (e.deleted) {
        delete map[e.id]
        delete reviews[e.id] // also clean up review state
      } else {
        map[e.id] = e
      }
      changed++
    }
  })

  // Full sync: remove local entries not in server response (orphans)
  if (serverIds) {
    Object.keys(map).forEach((id) => {
      if (!serverIds.has(id)) {
        delete map[id]
        delete reviews[id]
        changed++
      }
    })
  }

  setWords(map)
  setReviews(reviews)
  if (serverNow) setLastSync(serverNow)
  return { changed, total: Object.keys(map).length }
}

function wordList() {
  const map = getWords()
  return Object.keys(map)
    .map((id) => map[id])
    .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0))
}

/** Get a single word by ID */
function getWord(id) {
  const map = getWords()
  return map[id] || null
}

/** Parse the result JSON of a SyncEntry into structured data */
function parseResult(entry) {
  if (!entry || !entry.result) return null
  try {
    return JSON.parse(entry.result)
  } catch (e) {
    // result might be plain text
    return { raw: entry.result }
  }
}

// --- Storage error handling ---

function handleStorageError(e) {
  const msg = (e && e.errMsg) || String(e)
  if (msg.indexOf('exceed') >= 0 || msg.indexOf('quota') >= 0) {
    console.warn('Storage quota exceeded. Consider clearing old data.')
    // Could implement LRU eviction here in the future
  }
  console.error('Storage write failed:', msg)
}

// --- Daily review limit ---

function todayStr() {
  const d = new Date()
  return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0')
}

function getDailyLimit() {
  try { return wx.getStorageSync(DAILY_LIMIT_KEY) || 0 } catch (e) { return 0 }
}

function setDailyLimit(n) {
  try { wx.setStorageSync(DAILY_LIMIT_KEY, n) } catch (e) { handleStorageError(e) }
}

/** Get today's new-card count. Returns { date, newCount } */
function getDailyCount() {
  try {
    const c = wx.getStorageSync(DAILY_COUNT_KEY)
    if (c && c.date === todayStr()) return c
    return { date: todayStr(), newCount: 0 }
  } catch (e) {
    return { date: todayStr(), newCount: 0 }
  }
}

/** Increment today's new-card count by 1 */
function incrementDailyNewCount() {
  const c = getDailyCount()
  c.newCount++
  try { wx.setStorageSync(DAILY_COUNT_KEY, c) } catch (e) { handleStorageError(e) }
}

/** Decrement today's new-card count by 1 (for undo) */
function decrementDailyNewCount() {
  const c = getDailyCount()
  if (c.newCount > 0) c.newCount--
  try { wx.setStorageSync(DAILY_COUNT_KEY, c) } catch (e) { handleStorageError(e) }
}

/** Check if daily new-card limit is reached */
function isDailyNewLimitReached() {
  const limit = getDailyLimit()
  if (limit <= 0) return false
  return getDailyCount().newCount >= limit
}

module.exports = {
  getWords, setWords, mergePulled, wordList, getWord, parseResult,
  getReviews, setReviews, getReview, saveReview, removeReview,
  mergePulledReviews, getAllReviewCards,
  getLastSync, setLastSync,
  getDailyLimit, setDailyLimit, getDailyCount, incrementDailyNewCount, decrementDailyNewCount, isDailyNewLimitReached,
  WORDS_KEY, REVIEWS_KEY, LASTSYNC_KEY, DAILY_LIMIT_KEY, DAILY_COUNT_KEY
}
