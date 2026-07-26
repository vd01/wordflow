const sync = require('../../utils/sync')
const store = require('../../utils/store')
const fsrsEngine = require('../../utils/fsrs-engine')
const app = getApp()

function formatSyncTime(ts) {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  const now = new Date()
  const diffMs = now - d
  const diffMin = Math.floor(diffMs / 60000)
  if (diffMin < 1) return 'just now'
  if (diffMin < 60) return diffMin + ' min ago'
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return diffHr + ' hr ago'
  const diffDay = Math.floor(diffHr / 24)
  if (diffDay < 7) return diffDay + ' days ago'
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  const h = String(d.getHours()).padStart(2, '0')
  const min = String(d.getMinutes()).padStart(2, '0')
  return y + '-' + m + '-' + day + ' ' + h + ':' + min
}

function refreshCounts() {
  const dailyCount = store.getDailyCount()
  return {
    wordCount: store.wordList().length,
    dueCount: fsrsEngine.getQueueCounts(store.getWords(), store.getReviews()).total,
    lastSyncDisplay: formatSyncTime(store.getLastSync()),
    dailyLimit: store.getDailyLimit(),
    dailyNewCount: dailyCount.newCount
  }
}

Page({
  data: {
    token: '',
    status: '',
    busy: false,
    wordCount: 0,
    dueCount: 0,
    lastSyncDisplay: '',
    offline: false,
    // QR code login state
    loggedIn: false,       // whether user has a valid token
    pairingCode: '',       // scene/pairing code from desktop
    loginBusy: false       // login in progress
  },

  onLoad() {
    this.setData({
      token: app.globalData.token,
      loggedIn: !!app.globalData.token,
      ...refreshCounts()
    })

    // Check if we were launched from a QR code scan (scene parameter)
    const pendingScene = app.globalData.pendingScene
    if (pendingScene) {
      // Consume the pending scene
      app.globalData.pendingScene = null
      // Auto-trigger login with the scene
      this.handleSceneLogin(pendingScene)
    }

    // Wait for app's auto-sync to finish, then refresh UI
    if (app.globalData.syncPromise) {
      app.globalData.syncPromise.then((result) => {
        if (result) {
          this.setData({
            ...refreshCounts(),
            status: result.changed > 0 ? ('Synced ' + result.changed + ' entries') : ''
          })
        }
      })
    }

    this.checkNetwork()
    wx.onNetworkStatusChange((res) => {
      this.setData({ offline: !res.isConnected })
      if (res.isConnected && app.globalData.token) {
        this.doSync()
      }
    })
  },

  onShow() {
    this.setData({
      ...refreshCounts(),
      loggedIn: !!app.globalData.token
    })
  },

  checkNetwork() {
    const self = this
    wx.getNetworkType({
      success(res) {
        self.setData({ offline: res.networkType === 'none' })
      },
      fail() {}
    })
  },

  onDailyLimit(e) {
    const n = parseInt(e.detail.value, 10) || 0
    this.setData({ dailyLimit: n })
    store.setDailyLimit(n)
  },

  // Handle login when user scans QR code and mini program opens with scene
  async handleSceneLogin(scene) {
    this.setData({ loginBusy: true, status: 'Logging in via WeChat...', pairingCode: scene })
    try {
      const token = await app.doWeChatLogin(scene)
      this.setData({
        token: token,
        loggedIn: true,
        status: 'Login successful! Syncing...',
        pairingCode: ''
      })
      // Trigger initial sync
      this.doSync()
    } catch (e) {
      this.setData({
        status: 'Login failed: ' + e.message,
        pairingCode: ''
      })
    } finally {
      this.setData({ loginBusy: false })
    }
  },

  // Manual pairing code input (fallback if QR code scan doesn't work)
  onPairingCode(e) {
    this.setData({ pairingCode: e.detail.value.toUpperCase() })
  },

  // Login with manually entered pairing code
  async loginWithCode() {
    const scene = this.data.pairingCode.trim()
    if (!scene) {
      this.setData({ status: 'Please enter the pairing code from desktop app' })
      return
    }
    await this.handleSceneLogin(scene)
  },

  // Test connection
  async testConn() {
    if (this.data.offline) {
      this.setData({ status: 'No network connection' }); return
    }
    this.setData({ busy: true, status: 'Testing...' })
    const serverAddr = app.globalData.serverAddr
    try {
      const h = await sync.health(serverAddr)
      let status = 'Connected: ' + (h.service || 'wordflow-sync') + ' v' + (h.version || '?')
      if (h.wechat) {
        status += ' (WeChat auth enabled)'
      }
      if (this.data.token) {
        try {
          const st = await sync.getStatus(serverAddr, this.data.token)
          status += ' | Remote words: ' + st.wordCount
          this.setData({ wordCount: st.wordCount, lastSyncDisplay: formatSyncTime(st.lastSync) })
        } catch (e) {
          status += ' | Token invalid: ' + e.message
        }
      }
      this.setData({ status })
    } catch (e) {
      this.setData({ status: 'Connection failed: ' + e.message })
    } finally {
      this.setData({ busy: false })
    }
  },

  // Unified sync method (used by pullAll, auto-sync, network reconnect)
  async doSync(since) {
    const serverAddr = app.globalData.serverAddr
    const token = app.globalData.token
    if (!serverAddr || !token || this.data.offline) return

    this.setData({ busy: true, status: 'Syncing...' })
    try {
      const res = await sync.pull(serverAddr, token, since !== undefined ? since : (store.getLastSync() || 0))
      const r = store.mergePulled(res.entries || [], res.serverNow)
      this.setData({
        status: 'Synced ' + r.changed + ' entries, local total: ' + r.total,
        ...refreshCounts()
      })
    } catch (e) {
      this.setData({ status: 'Sync failed: ' + e.message })
    } finally {
      this.setData({ busy: false })
    }
  },

  pullAll() {
    this.doSync(0)
  },

  goWordList() {
    wx.navigateTo({ url: '/pages/wordlist/wordlist' })
  },

  goReview() {
    const dueCount = fsrsEngine.getQueueCounts(store.getWords(), store.getReviews()).total
    if (dueCount === 0) {
      wx.showToast({ title: 'No words to review', icon: 'none' }); return
    }
    wx.navigateTo({ url: '/pages/review/review' })
  }
})
