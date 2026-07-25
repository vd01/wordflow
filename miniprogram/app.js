const config = require('./utils/config')
const sync = require('./utils/sync')

App({
  globalData: {
    serverAddr: '', // e.g. https://sync.example.com (TLS reverse proxy -> SyncServer :9274)
    token: '',      // sync token from /api/v1/user/create
    serverNow: 0
  },

  onLaunch() {
    const cfg = config.load()
    this.globalData.serverAddr = cfg.serverAddr
    this.globalData.token = cfg.token
    // Lightweight health ping on launch (best-effort, ignored on failure)
    if (cfg.serverAddr) {
      sync.health(cfg.serverAddr).catch(() => {})
    }
  },

  setConfig(serverAddr, token) {
    this.globalData.serverAddr = serverAddr
    this.globalData.token = token
    config.save(serverAddr, token)
  }
})
