const store = require('../../utils/store')
const fsrsEngine = require('../../utils/fsrs-engine')

Page({
  data: {
    words: [],
    search: '',
    filtered: [],
    counts: { new: 0, learning: 0, review: 0, relearning: 0, total: 0 }
  },

  onShow() {
    this.refreshList()
  },

  refreshList() {
    const words = store.wordList()
    const reviews = store.getReviews()
    const counts = fsrsEngine.getQueueCounts(store.getWords(), reviews)

    // Enrich words with state info
    const enriched = words.map((w) => {
      const card = reviews[w.id]
      let state = 'new'
      let stateLabel = 'New'
      if (card) {
        state = card.state
        stateLabel = fsrsEngine.STATE_LABELS[card.state] || 'Unknown'
      }
      return Object.assign({}, w, { state, stateLabel })
    })

    this.setData({
      words: enriched,
      filtered: enriched,
      counts
    })
  },

  onSearch(e) {
    const q = (e.detail.value || '').trim().toLowerCase()
    const filtered = q
      ? this.data.words.filter((w) => (w.word || '').toLowerCase().indexOf(q) >= 0)
      : this.data.words
    this.setData({ search: q, filtered })
  },

  goDetail(e) {
    const id = e.currentTarget.dataset.id
    wx.navigateTo({ url: '/pages/worddetail/worddetail?id=' + id })
  },

  goReview() {
    wx.navigateTo({ url: '/pages/review/review' })
  },

  goBack() {
    wx.navigateBack()
  }
})
