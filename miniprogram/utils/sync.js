// SyncServer HTTP client — mirrors the WordWise syncserver API.
// Auth: token via Authorization: Bearer <token>.
// NOTE: production requires https + a whitelisted domain (TLS reverse proxy -> :9274).
// In dev, project.config.json sets setting.urlCheck=false to allow http.

function base(addr) {
  return (addr || '').trim().replace(/\/+$/, '')
}

function authHeader(token) {
  return {
    Authorization: 'Bearer ' + token,
    'Content-Type': 'application/json'
  }
}

function request({ url, method, header, data, timeout }) {
  return new Promise((resolve, reject) => {
    wx.request({
      url,
      method: method || 'GET',
      header: header || {},
      data: data,
      timeout: timeout || 15000,
      success: (res) => {
        if (res.statusCode >= 200 && res.statusCode < 300) {
          resolve(res.data)
        } else {
          const msg = (res.data && res.data.error) ? res.data.error : ('HTTP ' + res.statusCode)
          reject(new Error(msg))
        }
      },
      fail: (err) => reject(new Error(err.errMsg || 'request failed'))
    })
  })
}

// GET /api/v1/health  (public)
function health(serverAddr) {
  return request({ url: base(serverAddr) + '/api/v1/health' })
}

// GET /api/v1/user/status  (auth)
function getStatus(serverAddr, token) {
  return request({ url: base(serverAddr) + '/api/v1/user/status', header: authHeader(token) })
}

// GET /api/v1/sync/pull?since=<unix>  (auth). since=0 => all non-deleted.
function pull(serverAddr, token, since) {
  since = since || 0
  const q = since > 0 ? ('?since=' + since) : ''
  return request({ url: base(serverAddr) + '/api/v1/sync/pull' + q, header: authHeader(token) })
}

module.exports = { health, getStatus, pull, base, authHeader }
