const sync = require('../../utils/sync')
const store = require('../../utils/store')
const app = getApp()

Page({
  data: {
    serverAddr: '',
    token: '',
    status: '',
    busy: false,
    wordCount: 0,
    lastSync: 0
  },

  onLoad() {
    this.setData({
      serverAddr: app.globalData.serverAddr,
      token: app.globalData.token,
      wordCount: store.wordList().length,
      lastSync: store.getLastSync()
    })
  },

  onAddr(e) { this.setData({ serverAddr: e.detail.value }) },
  onToken(e) { this.setData({ token: e.detail.value }) },

  save() {
    app.setConfig(this.data.serverAddr, this.data.token)
    this.setData({ status: '已保存配置' })
  },

  async testConn() {
    if (!this.data.serverAddr) {
      this.setData({ status: '请填写服务器地址' }); return
    }
    this.setData({ busy: true, status: '测试中…' })
    try {
      const h = await sync.health(this.data.serverAddr)
      let status = '连接正常: ' + (h.service || 'wordwise-sync') + ' v' + (h.version || '?')
      if (this.data.token) {
        const st = await sync.getStatus(this.data.serverAddr, this.data.token)
        status += ' | 远程单词 ' + st.wordCount + ' 条'
        this.setData({ wordCount: st.wordCount, lastSync: st.lastSync })
      }
      this.setData({ status })
    } catch (e) {
      this.setData({ status: '失败: ' + e.message })
    } finally {
      this.setData({ busy: false })
    }
  },

  async pullAll() {
    if (!this.data.serverAddr || !this.data.token) {
      this.setData({ status: '请填写地址和 token' }); return
    }
    this.setData({ busy: true, status: '拉取中…' })
    try {
      const res = await sync.pull(this.data.serverAddr, this.data.token, 0)
      const r = store.mergePulled(res.entries || [], res.serverNow)
      this.setData({
        status: '已同步 ' + r.changed + ' 条，本地共 ' + r.total + ' 条',
        wordCount: r.total,
        lastSync: res.serverNow
      })
    } catch (e) {
      this.setData({ status: '拉取失败: ' + e.message })
    } finally {
      this.setData({ busy: false })
    }
  },

  comingSoon() {
    wx.showToast({ title: '后续阶段实现', icon: 'none' })
  }
})
