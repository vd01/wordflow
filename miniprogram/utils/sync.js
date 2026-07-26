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
          reject(new Error('Token invalid or expired, please re-login'))
        } else if (res.statusCode === 429) {
          reject(new Error('Too many requests, please try again later'))
        } else {
          const msg = (res.data && res.data.error) ? res.data.error : ('HTTP ' + res.statusCode)
          reject(new Error(msg))
        }
      },
      fail: (err) => {
        const msg = err.errMsg || ''
        if (msg.indexOf('url not in domain list') >= 0) {
          reject(new Error('Domain not whitelisted, check server address or enable dev mode'))
        } else if (msg.indexOf('timeout') >= 0 || msg.indexOf('request:fail') >= 0) {
          reject(new Error('Network connection failed, check network or server address'))
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

// POST /api/v1/auth/wechat/login  (public)
// Called by the mini program after wx.login() to authenticate with the server.
// { code: wx_login_code, scene: scene_from_qrcode }
// Returns: { token, message }
function wechatLogin(serverAddr, code, scene) {
  return request({
    url: base(serverAddr) + '/api/v1/auth/wechat/login',
    method: 'POST',
    data: { code: code, scene: scene }
  })
}

module.exports = { health, getStatus, pull, wechatLogin, base, authHeader }
