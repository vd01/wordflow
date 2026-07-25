// E2E test scaffolding for miniprogram-automator.
// Requires WeChat DevTools running with CLI/HTTP port enabled.
//
// Setup:
//   1. Open WeChat DevTools → 设置 → 安全设置 → enable 服务端口
//   2. Set CLI_PATH below to your DevTools cli.bat path
//   3. Set PROJECT_PATH to this miniprogram directory
//
// Run: npx jest __tests__/e2e.test.js --testTimeout=60000

const automator = require('miniprogram-automator')

// TODO: Update these paths for your environment
const CLI_PATH = process.env.WX_CLI_PATH || 'C:/Program Files (x86)/Tencent/微信web开发者工具/cli.bat'
const PROJECT_PATH = process.env.WX_PROJECT_PATH || __dirname + '/..'

let miniProgram = null

beforeAll(async () => {
  miniProgram = await automator.launch({
    cliPath: CLI_PATH,
    projectPath: PROJECT_PATH
  })
}, 60000)

afterAll(async () => {
  if (miniProgram) {
    await miniProgram.close()
  }
})

describe('E2E — Index page', () => {
  test('loads index page', async () => {
    const page = await miniProgram.currentPage()
    // Index page should have input elements for server address and token
    const inputs = await page.$$('.input')
    expect(inputs.length).toBeGreaterThanOrEqual(2)
  })
})

describe('E2E — Navigation', () => {
  test('navigates to word list page', async () => {
    const page = await miniProgram.currentPage()
    // Find and tap the word list button
    const btns = await page.$$('.btn')
    // The "单词本" button should navigate to wordlist page
    for (const btn of btns) {
      const text = await btn.text()
      if (text.includes('单词本')) {
        await btn.tap()
        break
      }
    }
    // Wait for navigation
    await page.waitFor(1000)
    const currentPage = await miniProgram.currentPage()
    expect(currentPage.path).toContain('wordlist')
  })
})

// NOTE: Full E2E review flow test requires a running SyncServer with test data.
// Add more tests here once the test environment is set up.
