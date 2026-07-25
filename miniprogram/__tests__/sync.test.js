// Unit tests for utils/sync.js — HTTP client (mocked wx.request)

const sync = require('../utils/sync')

// Mock wx.request for sync tests
let mockResponse = { statusCode: 200, data: {} }
let mockRequestOptions = null

beforeEach(() => {
  mockResponse = { statusCode: 200, data: {} }
  mockRequestOptions = null

  global.wx.request = (opts) => {
    mockRequestOptions = opts
    if (mockResponse.statusCode >= 200 && mockResponse.statusCode < 300) {
      opts.success(mockResponse)
    } else {
      opts.success(mockResponse) // sync.js handles status check
    }
  }
})

describe('sync — base URL helper', () => {
  test('trims trailing slashes', () => {
    expect(sync.base('http://localhost:9274/')).toBe('http://localhost:9274')
    expect(sync.base('http://localhost:9274///')).toBe('http://localhost:9274')
  })

  test('trims whitespace', () => {
    expect(sync.base('  http://localhost:9274  ')).toBe('http://localhost:9274')
  })

  test('handles empty string', () => {
    expect(sync.base('')).toBe('')
  })
})

describe('sync — authHeader', () => {
  test('produces Bearer token header', () => {
    const h = sync.authHeader('mytoken123')
    expect(h.Authorization).toBe('Bearer mytoken123')
    expect(h['Content-Type']).toBe('application/json')
  })
})

describe('sync — health', () => {
  test('calls GET /api/v1/health', async () => {
    mockResponse.data = { status: 'ok', service: 'wordwise-sync', version: '1.0' }
    const result = await sync.health('http://localhost:9274')
    expect(mockRequestOptions.url).toBe('http://localhost:9274/api/v1/health')
    expect(mockRequestOptions.method).toBe('GET')
    expect(result.service).toBe('wordwise-sync')
  })
})

describe('sync — getStatus', () => {
  test('calls GET /api/v1/user/status with auth header', async () => {
    mockResponse.data = { token: 'abc', wordCount: 42, lastSync: 1000 }
    const result = await sync.getStatus('http://localhost:9274', 'abc')
    expect(mockRequestOptions.url).toBe('http://localhost:9274/api/v1/user/status')
    expect(mockRequestOptions.header.Authorization).toBe('Bearer abc')
    expect(result.wordCount).toBe(42)
  })
})

describe('sync — pull', () => {
  test('calls GET /api/v1/sync/pull with since param', async () => {
    mockResponse.data = { entries: [], serverNow: 9999 }
    const result = await sync.pull('http://localhost:9274', 'tok', 5000)
    expect(mockRequestOptions.url).toBe('http://localhost:9274/api/v1/sync/pull?since=5000')
    expect(result.serverNow).toBe(9999)
  })

  test('pull with since=0 omits query param', async () => {
    mockResponse.data = { entries: [], serverNow: 0 }
    await sync.pull('http://localhost:9274', 'tok', 0)
    expect(mockRequestOptions.url).toBe('http://localhost:9274/api/v1/sync/pull')
  })
})
