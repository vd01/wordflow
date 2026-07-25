// Unit tests for utils/store.js — word storage + review state CRUD

const store = require('../utils/store')

beforeEach(() => {
  global.__mockStorage = {}
  global.wx.clearStorageSync()
})

describe('store — word operations', () => {
  test('getWords returns empty object when no data', () => {
    expect(store.getWords()).toEqual({})
  })

  test('mergePulled adds entries and returns counts', () => {
    const entries = [
      { id: '1', word: 'hello', result: '{"word":"hello"}', createdAt: 1000, updatedAt: 1000, deleted: false },
      { id: '2', word: 'world', result: '{"word":"world"}', createdAt: 2000, updatedAt: 2000, deleted: false }
    ]
    const r = store.mergePulled(entries, 3000)
    expect(r.changed).toBe(2)
    expect(r.total).toBe(2)
  })

  test('mergePulled last-write-wins by updatedAt', () => {
    store.mergePulled([
      { id: '1', word: 'hello', result: 'v1', createdAt: 1000, updatedAt: 1000, deleted: false }
    ], 1000)

    const r = store.mergePulled([
      { id: '1', word: 'hello', result: 'v2', createdAt: 1000, updatedAt: 2000, deleted: false }
    ], 2000)

    expect(r.changed).toBe(1)
    expect(store.getWord('1').result).toBe('v2')
  })

  test('mergePulled removes soft-deleted entries', () => {
    store.mergePulled([
      { id: '1', word: 'hello', result: 'v1', createdAt: 1000, updatedAt: 1000, deleted: false }
    ], 1000)

    store.mergePulled([
      { id: '1', word: 'hello', result: '', createdAt: 1000, updatedAt: 2000, deleted: true }
    ], 2000)

    expect(store.getWord('1')).toBeNull()
  })

  test('mergePulled also cleans up review state for deleted words', () => {
    store.mergePulled([
      { id: '1', word: 'hello', result: 'v1', createdAt: 1000, updatedAt: 1000, deleted: false }
    ], 1000)
    store.saveReview('1', { id: '1', state: 0 })

    store.mergePulled([
      { id: '1', word: 'hello', result: '', createdAt: 1000, updatedAt: 2000, deleted: true }
    ], 2000)

    expect(store.getReview('1')).toBeNull()
  })

  test('wordList returns sorted by createdAt desc', () => {
    store.mergePulled([
      { id: '1', word: 'first', result: '', createdAt: 1000, updatedAt: 1000, deleted: false },
      { id: '2', word: 'second', result: '', createdAt: 2000, updatedAt: 2000, deleted: false }
    ], 2000)

    const list = store.wordList()
    expect(list[0].word).toBe('second')
    expect(list[1].word).toBe('first')
  })

  test('getWord returns entry by id', () => {
    store.mergePulled([
      { id: 'abc', word: 'test', result: 'data', createdAt: 1000, updatedAt: 1000, deleted: false }
    ], 1000)

    expect(store.getWord('abc').word).toBe('test')
    expect(store.getWord('nonexistent')).toBeNull()
  })
})

describe('store — parseResult', () => {
  test('parses valid JSON result', () => {
    const entry = { result: '{"word":"hello","phonetic":"həˈloʊ","translation":"n. 你好"}' }
    const parsed = store.parseResult(entry)
    expect(parsed.word).toBe('hello')
    expect(parsed.phonetic).toBe('həˈloʊ')
    expect(parsed.translation).toBe('n. 你好')
  })

  test('returns {raw} for non-JSON result', () => {
    const entry = { result: 'plain text result' }
    const parsed = store.parseResult(entry)
    expect(parsed.raw).toBe('plain text result')
  })

  test('returns null for null/undefined entry', () => {
    expect(store.parseResult(null)).toBeNull()
    expect(store.parseResult(undefined)).toBeNull()
    expect(store.parseResult({})).toBeNull()
  })
})

describe('store — review state CRUD', () => {
  test('saveReview / getReview round-trip', () => {
    const card = { id: '1', state: 0, due: '2025-01-01T00:00:00.000Z', stability: 0, difficulty: 0, reps: 0, lapses: 0 }
    store.saveReview('1', card)
    expect(store.getReview('1')).toEqual(card)
  })

  test('getReview returns null for unknown id', () => {
    expect(store.getReview('nope')).toBeNull()
  })

  test('removeReview deletes review state', () => {
    store.saveReview('1', { id: '1', state: 0 })
    store.removeReview('1')
    expect(store.getReview('1')).toBeNull()
  })

  test('getReviews returns all review cards', () => {
    store.saveReview('1', { id: '1', state: 0 })
    store.saveReview('2', { id: '2', state: 2 })
    const all = store.getReviews()
    expect(Object.keys(all).length).toBe(2)
  })
})

describe('store — lastSync', () => {
  test('getLastSync defaults to 0', () => {
    expect(store.getLastSync()).toBe(0)
  })

  test('setLastSync / getLastSync round-trip', () => {
    store.setLastSync(1234567890)
    expect(store.getLastSync()).toBe(1234567890)
  })
})
