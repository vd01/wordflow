// Local config persistence (server address + sync token) in wx storage.
const STORAGE_KEY = 'wordwise_config'

function load() {
  try {
    const v = wx.getStorageSync(STORAGE_KEY)
    if (v && typeof v === 'object') {
      return { serverAddr: v.serverAddr || '', token: v.token || '' }
    }
  } catch (e) {}
  return { serverAddr: '', token: '' }
}

function save(serverAddr, token) {
  try {
    wx.setStorageSync(STORAGE_KEY, { serverAddr, token })
  } catch (e) {}
}

module.exports = { load, save, STORAGE_KEY }
