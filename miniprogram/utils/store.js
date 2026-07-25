// Local storage of words pulled from SyncServer + (Phase 4) FSRS review state.
// Review state is Mini-Program-LOCAL (decision A): not synced to the server.

const WORDS_KEY = 'wordwise_words'     // { [id]: SyncEntry }
const REVIEWS_KEY = 'wordwise_reviews' // { [id]: FsrsCard }  (filled in Phase 4)
const LASTSYNC_KEY = 'wordwise_lastsync' // last successful pull (serverNow, unix seconds)

function getWords() {
  try { return wx.getStorageSync(WORDS_KEY) || {} } catch (e) { return {} }
}
function setWords(map) {
  try { wx.setStorageSync(WORDS_KEY, map) } catch (e) {}
}
function getLastSync() {
  try { return wx.getStorageSync(LASTSYNC_KEY) || 0 } catch (e) { return 0 }
}
function setLastSync(ts) {
  try { wx.setStorageSync(LASTSYNC_KEY, ts) } catch (e) {}
}

// Merge pulled entries into local store (last-write-wins by updatedAt; soft-deletes remove).
function mergePulled(entries, serverNow) {
  const map = getWords()
  let changed = 0
  ;(entries || []).forEach((e) => {
    const cur = map[e.id]
    if (!cur || (e.updatedAt || 0) > (cur.updatedAt || 0)) {
      if (e.deleted) { delete map[e.id] } else { map[e.id] = e }
      changed++
    }
  })
  setWords(map)
  if (serverNow) setLastSync(serverNow)
  return { changed, total: Object.keys(map).length }
}

function wordList() {
  const map = getWords()
  return Object.keys(map)
    .map((id) => map[id])
    .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0))
}

module.exports = {
  getWords, setWords, mergePulled, wordList,
  getLastSync, setLastSync,
  WORDS_KEY, REVIEWS_KEY, LASTSYNC_KEY
}
