// Local config persistence (sync token) in wx storage.
const STORAGE_KEY = 'wordwise_config'

// Fixed sync server URL — not user-configurable
const SERVER_ADDR = 'https://word-flow.duckdns.org:31588/'

function load() {
  try {
    const v = wx.getStorageSync(STORAGE_KEY)
    if (v && typeof v === 'object') {
      return { serverAddr: SERVER_ADDR, token: v.token || '' }
    }
  } catch (e) {}
  return { serverAddr: SERVER_ADDR, token: '' }
}

function save(token) {
  try {
    wx.setStorageSync(STORAGE_KEY, { serverAddr: SERVER_ADDR, token })
  } catch (e) {}
}

module.exports = { load, save, SERVER_ADDR, STORAGE_KEY }
