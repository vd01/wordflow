module.exports = {
  testEnvironment: 'node',
  roots: ['<rootDir>/__tests__'],
  testMatch: ['**/*.test.js'],
  verbose: true,
  // Mock wx global for unit tests
  setupFiles: ['<rootDir>/__tests__/setup.js']
}
