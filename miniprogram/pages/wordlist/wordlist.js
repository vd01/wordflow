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

    // Enrich words with state info + audioUrl from parsed result
    const enriched = words.map((w) => {
      const card = reviews[w.id]
      let state = 'new'
      let stateLabel = 'New'
      if (card) {
        state = card.state
        stateLabel = fsrsEngine.STATE_LABELS[card.state] || 'Unknown'
      }
      const parsed = store.parseResult(w)
      const audioUrl = parsed?.audioUrl || ''
      return Object.assign({}, w, { state, stateLabel, audioUrl })
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
  },

  playPronunciation(e) {
    const { word, audioUrl } = e.currentTarget.dataset
    if (!word) return

    if (this._audio) {
      this._audio.destroy()
      this._audio = null
    }

    const audio = wx.createInnerAudioContext()
    if (audioUrl) {
      audio.src = audioUrl
      audio.onError(() => {
        const fallback = wx.createInnerAudioContext()
        fallback.src = 'https://dict.youdao.com/dictvoice?audio=' + encodeURIComponent(word) + '&type=1'
        fallback.play()
        this._audio = fallback
      })
      audio.play()
    } else {
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
