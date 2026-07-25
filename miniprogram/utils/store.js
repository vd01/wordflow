// Local storage of words pulled from SyncServer + FSRS review state.
// Review state is Mini-Program-LOCAL (decision A): not synced to the server.

const WORDS_KEY = 'wordwise_words'     // { [id]: SyncEntry }
const REVIEWS_KEY = 'wordwise_reviews' // { [id]: FsrsCard }
const LASTSYNC_KEY = 'wordwise_lastsync' // last successful pull (serverNow, unix seconds)

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

// --- Word operations ---

// Merge pulled entries into local store (last-write-wins by updatedAt; soft-deletes remove).
function mergePulled(entries, serverNow) {
  const map = getWords()
  const reviews = getReviews()
  let changed = 0
  ;(entries || []).forEach((e) => {
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

module.exports = {
  getWords, setWords, mergePulled, wordList, getWord, parseResult,
  getReviews, setReviews, getReview, saveReview, removeReview,
  getLastSync, setLastSync,
  WORDS_KEY, REVIEWS_KEY, LASTSYNC_KEY
}
