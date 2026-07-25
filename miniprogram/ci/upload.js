// miniprogram-ci script: build npm + upload + preview QR
// Usage:
//   node ci/upload.js --env preview   (upload preview version, generates QR)
//   node ci/upload.js --env upload    (upload formal version)
//
// Prerequisites:
//   1. npm install miniprogram-ci as devDependency
//   2. Place your private.key in ci/private.key (gitignored)
//   3. Set APPID and VERSION below

const path = require('path')

// Config — update these
const APPID = 'wxcf0e31a667b6fe79'
const VERSION = '0.1.0'
const DESC = 'WordWise Mini Program — FSRS flashcards'
const PROJECT_PATH = path.resolve(__dirname, '..')
const PRIVATE_KEY_PATH = path.resolve(__dirname, 'private.key')

async function main() {
  let ci
  try {
    ci = require('miniprogram-ci')
  } catch (e) {
    console.error('miniprogram-ci not installed. Run: npm install miniprogram-ci --save-dev')
    process.exit(1)
  }

  const env = (process.argv.find(a => a.startsWith('--env=')) || '--env=preview').split('=')[1]

  const project = new ci.Project({
    appid: APPID,
    type: 'miniProgram',
    projectPath: PROJECT_PATH,
    privateKeyPath: PRIVATE_KEY_PATH,
    ignores: ['node_modules', '__tests__', 'ci', 'jest.config.js', 'package.json', 'package-lock.json']
  })

  if (env === 'preview') {
    console.log('Generating preview QR...')
    const previewResult = await ci.preview({
      project,
      desc: DESC,
      qrcodeFormat: 'terminal',
      setting: {
        es6: true,
        enhance: true,
        minify: true
      }
    })
    console.log('Preview generated. Scan QR code with WeChat to test.')
  } else {
    console.log('Uploading version ' + VERSION + '...')
    const uploadResult = await ci.upload({
      project,
      version: VERSION,
      desc: DESC,
      setting: {
        es6: true,
        enhance: true,
        minify: true
      }
    })
    console.log('Upload complete:', uploadResult)
  }
}

main().catch(err => {
  console.error('CI failed:', err)
  process.exit(1)
})
