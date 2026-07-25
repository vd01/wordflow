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
  if (diffMin < 1) return '刚刚'
  if (diffMin < 60) return diffMin + ' 分钟前'
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return diffHr + ' 小时前'
  const diffDay = Math.floor(diffHr / 24)
  if (diffDay < 7) return diffDay + ' 天前'
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
    serverAddr: '',
    token: '',
    status: '',
    busy: false,
    wordCount: 0,
    dueCount: 0,
    lastSyncDisplay: '',
    offline: false
  },

  onLoad() {
    this.setData({
      serverAddr: app.globalData.serverAddr,
      token: app.globalData.token,
      ...refreshCounts()
    })

    // Wait for app's auto-sync to finish, then refresh UI
    if (app.globalData.syncPromise) {
      app.globalData.syncPromise.then((result) => {
        if (result) {
          this.setData({
            ...refreshCounts(),
            status: result.changed > 0 ? ('已同步 ' + result.changed + ' 条') : ''
          })
        }
      })
    }

    this.checkNetwork()
    wx.onNetworkStatusChange((res) => {
      this.setData({ offline: !res.isConnected })
      if (res.isConnected && this.data.serverAddr) {
        this.doSync()
      }
    })
  },

  onShow() {
    this.setData({ ...refreshCounts() })
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

  onAddr(e) { this.setData({ serverAddr: e.detail.value }) },
  onToken(e) { this.setData({ token: e.detail.value }) },
  onDailyLimit(e) {
    const n = parseInt(e.detail.value, 10) || 0
    this.setData({ dailyLimit: n })
    store.setDailyLimit(n)
  },

  save() {
    if (!this.data.serverAddr) {
      this.setData({ status: '请填写服务器地址' }); return
    }
    app.setConfig(this.data.serverAddr, this.data.token)
    this.setData({ status: '配置已保存' })
  },

  async testConn() {
    if (!this.data.serverAddr) {
      this.setData({ status: '请填写服务器地址' }); return
    }
    if (this.data.offline) {
      this.setData({ status: '当前无网络连接' }); return
    }
    this.setData({ busy: true, status: '测试中…' })
    try {
      const h = await sync.health(this.data.serverAddr)
      let status = '连接正常: ' + (h.service || 'wordwise-sync') + ' v' + (h.version || '?')
      if (this.data.token) {
        try {
          const st = await sync.getStatus(this.data.serverAddr, this.data.token)
          status += ' | 远程单词 ' + st.wordCount + ' 条'
          this.setData({ wordCount: st.wordCount, lastSyncDisplay: formatSyncTime(st.lastSync) })
        } catch (e) {
          status += ' | Token 验证失败: ' + e.message
        }
      }
      this.setData({ status })
    } catch (e) {
      this.setData({ status: '连接失败: ' + e.message })
    } finally {
      this.setData({ busy: false })
    }
  },

  // Unified sync method (used by pullAll, auto-sync, network reconnect)
  async doSync(since) {
    const { serverAddr, token, offline } = this.data
    if (!serverAddr || !token || offline) return

    this.setData({ busy: true, status: '同步中…' })
    try {
      const res = await sync.pull(serverAddr, token, since !== undefined ? since : (store.getLastSync() || 0))
      const r = store.mergePulled(res.entries || [], res.serverNow)
      this.setData({
        status: '已同步 ' + r.changed + ' 条，本地共 ' + r.total + ' 条',
        ...refreshCounts()
      })
    } catch (e) {
      this.setData({ status: '同步失败: ' + e.message })
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
      wx.showToast({ title: '暂无待复习单词', icon: 'none' }); return
    }
    wx.navigateTo({ url: '/pages/review/review' })
  }
})
