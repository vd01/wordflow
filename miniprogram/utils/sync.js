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
        } else if (res.statusCode === 401) {
          reject(new Error('Token 无效或已过期，请重新配置'))
        } else if (res.statusCode === 429) {
          reject(new Error('请求过于频繁，请稍后再试'))
        } else {
          const msg = (res.data && res.data.error) ? res.data.error : ('HTTP ' + res.statusCode)
          reject(new Error(msg))
        }
      },
      fail: (err) => {
        const msg = err.errMsg || ''
        if (msg.indexOf('url not in domain list') >= 0) {
          reject(new Error('域名未加入白名单，请检查服务器地址或开启开发模式'))
        } else if (msg.indexOf('timeout') >= 0 || msg.indexOf('request:fail') >= 0) {
          reject(new Error('网络连接失败，请检查网络或服务器地址'))
        } else {
          reject(new Error(msg || 'request failed'))
        }
      }
    })
  })
}

// GET /api/v1/health  (public)
function health(serverAddr) {
  return request({ url: base(serverAddr) + '/api/v1/health', timeout: 8000 })
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
