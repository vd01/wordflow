// Local config persistence (server address + sync token) in wx storage.
const STORAGE_KEY = 'wordwise_config'

const DEFAULT_SERVER = 'https://vocab-agent.duckdns.org:31588/'

function load() {
  try {
    const v = wx.getStorageSync(STORAGE_KEY)
    if (v && typeof v === 'object') {
      return { serverAddr: v.serverAddr || DEFAULT_SERVER, token: v.token || '' }
    }
  } catch (e) {}
  return { serverAddr: DEFAULT_SERVER, token: '' }
}

function save(serverAddr, token) {
  try {
    wx.setStorageSync(STORAGE_KEY, { serverAddr, token })
  } catch (e) {}
}

module.exports = { load, save, STORAGE_KEY }
