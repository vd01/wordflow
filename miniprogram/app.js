const config = require('./utils/config')
const sync = require('./utils/sync')
const store = require('./utils/store')
const fsrsEngine = require('./utils/fsrs-engine')

App({
  globalData: {
    serverAddr: '',
    token: '',
    serverNow: 0,
    syncPromise: null  // ongoing sync promise, pages can await it
  },

  onLaunch() {
    const cfg = config.load()
    this.globalData.serverAddr = cfg.serverAddr
    this.globalData.token = cfg.token

    // Start auto sync (pages can await globalData.syncPromise)
    this.globalData.syncPromise = this.autoSync()

    // If server is configured and there are due cards, go straight to review
    if (cfg.serverAddr && cfg.token) {
      const words = store.getWords()
      const reviews = store.getReviews()
      const dueCount = fsrsEngine.getQueueCounts(words, reviews).total
      if (dueCount > 0) {
        wx.redirectTo({ url: '/pages/review/review' })
      }
    }
  },

  setConfig(serverAddr, token) {
    this.globalData.serverAddr = serverAddr
    this.globalData.token = token
    config.save(serverAddr, token)
  },

  // Auto sync on launch: full pull if never synced, delta otherwise
  async autoSync() {
    const { serverAddr, token } = this.globalData
    if (!serverAddr || !token) return null

    const since = store.getLastSync()
    try {
      const res = await sync.pull(serverAddr, token, since || 0)
      const r = store.mergePulled(res.entries || [], res.serverNow)
      if (r.changed > 0) {
        console.log('Auto sync: ' + r.changed + ' entries updated')
      }
      return { changed: r.changed, total: r.total, serverNow: res.serverNow }
    } catch (e) {
      // Best-effort, silent failure
      return null
    }
  }
})
