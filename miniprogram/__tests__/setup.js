// Mock WeChat Mini Program wx global for Jest unit tests.
// Only the APIs used by our utils are mocked.

const storage = {}

global.wx = {
  getStorageSync(key) {
    return storage[key] !== undefined ? storage[key] : ''
  },
  setStorageSync(key, value) {
    storage[key] = value
  },
  removeStorageSync(key) {
    delete storage[key]
  },
  clearStorageSync() {
    for (const k of Object.keys(storage)) delete storage[k]
  },
  request() {
    // Not used in unit tests; E2E tests use real DevTools
  },
  showToast() {},
  navigateTo() {},
  navigateBack() {}
}

// Expose storage for test assertions / resets
global.__mockStorage = storage
