const fsrsEngine = require('../../utils/fsrs-engine')
const store = require('../../utils/store')
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
    errorMsg: ''
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
      const queue = fsrsEngine.getDueQueue(words, reviews)

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
        errorMsg: ''
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
  }
})
