const fsrsEngine = require('../../utils/fsrs-engine')
const store = require('../../utils/store')
const sync = require('../../utils/sync')
const app = getApp()

Page({
  data: {
    // Queue state
    queue: [],
    currentIndex: 0,
    remaining: 0,

    // Session stats (accumulated during this review session)
    reviewed: 0,
    sessionAgain: 0,
    sessionHard: 0,
    sessionGood: 0,
    sessionEasy: 0,

    // Remaining due after session
    remainingDue: 0,

    // Current card
    entry: null,
    card: null,
    parsedResult: null,
    revealed: false,

    // Preview intervals for rating buttons
    intervals: { again: '', hard: '', good: '', easy: '' },

    // Undo
    undoStack: [],

    // Empty state
    emptyState: false,

    // Error state
    errorMsg: '',

    // Daily limit
    dailyLimit: 0,
    dailyNewCount: 0,
    dailyNewRemaining: -1
  },

  onLoad() {
    if (app.globalData.syncPromise) {
      app.globalData.syncPromise.then(() => {
        this.buildQueue()
      })
    } else {
      this.buildQueue()
    }
  },

  onShow() {
    this.buildQueue()
  },

  buildQueue() {
    try {
      const words = store.getWords()
      const reviews = store.getReviews()
      const limit = store.getDailyLimit()
      const dailyCount = store.getDailyCount()
      // Calculate how many new cards are still allowed today
      let dailyNewRemaining = -1 // -1 = unlimited
      if (limit > 0) {
        dailyNewRemaining = Math.max(0, limit - dailyCount.newCount)
      }
      const queue = fsrsEngine.getDueQueue(words, reviews, dailyNewRemaining)
      const counts = fsrsEngine.getQueueCounts(words, reviews)

      this.setData({
        queue,
        currentIndex: 0,
        remaining: queue.length,
        reviewed: 0,
        sessionAgain: 0,
        sessionHard: 0,
        sessionGood: 0,
        sessionEasy: 0,
        remainingDue: 0,
        emptyState: queue.length === 0,
        undoStack: [],
        errorMsg: '',
        dailyLimit: limit,
        dailyNewCount: dailyCount.newCount,
        dailyNewRemaining: dailyNewRemaining
      })

      if (queue.length > 0) {
        this.showCard(0)
      } else {
        this.setData({ entry: null, card: null, parsedResult: null, revealed: false })
      }
    } catch (e) {
      this.setData({ errorMsg: '加载复习队列失败: ' + e.message })
    }
  },

  showCard(index) {
    const queue = this.data.queue
    if (index >= queue.length) {
      this.showDoneScreen()
      return
    }

    const id = queue[index]
    const entry = store.getWord(id)
    if (!entry) {
      this.showCard(index + 1)
      return
    }

    const parsedResult = store.parseResult(entry)
    let card = store.getReview(id)
    if (!card) {
      card = fsrsEngine.createCard(id)
    }

    const hasContent = !!(parsedResult && (
      parsedResult.translation || parsedResult.definition ||
      (parsedResult.definitions && parsedResult.definitions.length > 0) ||
      parsedResult.memory_tips || parsedResult.synonyms ||
      parsedResult.antonyms || parsedResult.etymology ||
      parsedResult.exchange || parsedResult.pos || parsedResult.tag
    ))

    let intervals = { again: '', hard: '', good: '', easy: '' }
    try {
      const preview = fsrsEngine.previewIntervals(card)
      intervals = {
        again: fsrsEngine.formatInterval(preview.again.card.scheduled_days),
        hard:  fsrsEngine.formatInterval(preview.hard.card.scheduled_days),
        good:  fsrsEngine.formatInterval(preview.good.card.scheduled_days),
        easy:  fsrsEngine.formatInterval(preview.easy.card.scheduled_days)
      }
    } catch (e) {}

    this.setData({
      entry,
      card,
      parsedResult,
      hasContent,
      revealed: false,
      intervals,
      currentIndex: index,
      remaining: queue.length - index,
      errorMsg: ''
    })
  },

  showDoneScreen() {
    // Calculate remaining due cards after this session
    const words = store.getWords()
    const reviews = store.getReviews()
    const remainingDue = fsrsEngine.getQueueCounts(words, reviews).total

    this.setData({
      emptyState: true,
      entry: null,
      card: null,
      parsedResult: null,
      remainingDue
    })

    // Push review cards to server in background
    this.pushReviewsAsync()
  },

  pushReviewsAsync() {
    const serverAddr = getApp().globalData.serverAddr
    const token = getApp().globalData.token
    if (!serverAddr || !token) return
    const localCards = store.getAllReviewCards()
    if (localCards.length === 0) return
    sync.pushReviews(serverAddr, token, localCards).catch(() => { /* best effort */ })
  },

  reveal() {
    this.setData({ revealed: true })
  },

  rate(e) {
    const rating = parseInt(e.currentTarget.dataset.rating)
    const { card, queue, currentIndex, reviewed, undoStack } = this.data

    if (!card) return
    if (![1, 2, 3, 4].includes(rating)) return

    // Track session stats for undo
    const sessionSnap = {
      sessionAgain: this.data.sessionAgain,
      sessionHard: this.data.sessionHard,
      sessionGood: this.data.sessionGood,
      sessionEasy: this.data.sessionEasy
    }

    undoStack.push({
      index: currentIndex,
      card: JSON.parse(JSON.stringify(card)),
      reviewed: reviewed,
      sessionSnap
    })

    // Apply rating
    try {
      const result = fsrsEngine.rateCard(card, rating)
      store.saveReview(card.id || this.data.entry.id, result.card)
      // Track new card count for daily limit
      if (!card || card.state === 0) { // STATE_NEW = 0
        store.incrementDailyNewCount()
      }
    } catch (e) {
      this.setData({ errorMsg: '评分处理失败，请重试' })
      undoStack.pop()
      return
    }

    // Update session stats
    const statKey = rating === 1 ? 'sessionAgain' : rating === 2 ? 'sessionHard' : rating === 3 ? 'sessionGood' : 'sessionEasy'
    const statUpdate = {}
    statUpdate[statKey] = this.data[statKey] + 1

    const nextIndex = currentIndex + 1
    const newRemaining = queue.length - nextIndex

    this.setData({
      reviewed: reviewed + 1,
      undoStack,
      remaining: newRemaining,
      ...statUpdate
    })

    if (nextIndex < queue.length) {
      this.showCard(nextIndex)
    } else {
      this.showDoneScreen()
    }
  },

  undo() {
    const { undoStack } = this.data
    if (undoStack.length === 0) return

    const last = undoStack.pop()

    try {
      store.saveReview(last.card.id, last.card)
      // Undo new card count if the undone card was new
      if (last.card.state === 0) { // STATE_NEW = 0
        store.decrementDailyNewCount()
      }
    } catch (e) {}

    this.setData({
      undoStack,
      reviewed: last.reviewed,
      sessionAgain: last.sessionSnap.sessionAgain,
      sessionHard: last.sessionSnap.sessionHard,
      sessionGood: last.sessionSnap.sessionGood,
      sessionEasy: last.sessionSnap.sessionEasy,
      currentIndex: last.index,
      remaining: this.data.queue.length - last.index
    })

    this.showCard(last.index)
  },

  goSettings() {
    wx.redirectTo({ url: '/pages/index/index' })
  },

  goBack() {
    const pages = getCurrentPages()
    if (pages.length > 1) {
      wx.navigateBack()
    } else {
      wx.redirectTo({ url: '/pages/index/index' })
    }
  },

  restart() {
    this.buildQueue()
  },

  // ── Pronunciation ──
  // Strategy: play audioUrl from synced result (real MP3 from Free Dictionary API,
  // saved by the desktop app). If no audioUrl, fall back to Youdao TTS.
  playPronunciation() {
    const { entry, parsedResult } = this.data
    if (!entry && !parsedResult) return

    const word = parsedResult?.word || entry?.word || ''
    if (!word) return

    // Destroy previous audio if any
    if (this._audio) {
      this._audio.destroy()
      this._audio = null
    }

    const audioUrl = parsedResult?.audioUrl
    const audio = wx.createInnerAudioContext()

    if (audioUrl) {
      // Real human recording from Free Dictionary API (synced from desktop)
      audio.src = audioUrl
      audio.onError(() => {
        // Real audio failed -> fall back to Youdao TTS
        console.log('[pronounce] Real audio failed, falling back to Youdao TTS')
        const fallback = wx.createInnerAudioContext()
        fallback.src = 'https://dict.youdao.com/dictvoice?audio=' + encodeURIComponent(word) + '&type=1'
        fallback.play()
        this._audio = fallback
      })
      audio.play()
    } else {
      // No real recording -> Youdao TTS (American accent)
      audio.src = 'https://dict.youdao.com/dictvoice?audio=' + encodeURIComponent(word) + '&type=1'
      audio.play()
    }

    this._audio = audio
  },

  onUnload() {
    if (this._audio) {
      this._audio.destroy()
      this._audio = null
    }
  }
})
