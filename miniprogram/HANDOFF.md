# WordFlow Mini Program — Handoff Documentation

## Overview

A WeChat Mini Program that pulls word data from the WordFlow SyncServer and implements Anki-like spaced-repetition review using the FSRS-6 algorithm.

**AppID:** `wxcf0e31a667b6fe79`

## Architecture

```
miniprogram/
├── app.js              — App entry: loads config, auto delta-sync on launch
├── app.json            — Page routes + window config
├── app.wxss            — Global styles
├── project.config.json — WeChat project config (appid, build settings, pack ignore)
├── sitemap.json
├── pages/
│   ├── index/          — Home: server config, sync, navigation
│   ├── wordlist/       — Word list: search, state badges, start review
│   └── review/         — Flashcard review: FSRS scheduling, rating, undo
├── utils/
│   ├── config.js       — Server address + token persistence (wx storage)
│   ├── sync.js         — SyncServer HTTP client (health, getStatus, pull)
│   ├── store.js        — Local word cache + FSRS review state (wx storage)
│   ├── fsrs-engine.js  — FSRS wrapper (createCard, rateCard, dueQueue, preview)
│   └── ts-fsrs.js      — Vendored ts-fsrs v5.4.1 CJS bundle (62KB)
├── __tests__/          — Jest unit tests (42 tests, all passing)
├── ci/                 — miniprogram-ci upload/preview script
├── jest.config.js
└── package.json
```

## Data Flow

1. **Config** → User enters server address + token on index page → saved to `wx.storage`
2. **Sync** → `sync.pull()` fetches entries from SyncServer → `store.mergePulled()` merges into local cache
3. **Review** → `fsrsEngine.getDueQueue()` scans words + review state → builds due queue
4. **Rate** → User taps Again/Hard/Good/Easy → `fsrsEngine.rateCard()` updates FSRS card → `store.saveReview()` persists
5. **Auto-sync** → On app launch, `autoDeltaSync()` pulls changes since last sync

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| FSRS state is Mini-Program-LOCAL | No SyncServer changes needed; cross-device sync not required for v1 |
| ts-fsrs vendored as CJS | WeChat "Build NPM" can be unreliable; vendoring is simpler |
| Pull-only sync (no push) | Phone is a read-only consumer of SyncServer words |
| TLS reverse proxy for prod | Mini Program requires https + whitelisted domain |
| `position: fixed` for rating buttons | Most reliable bottom-pinning in WeChat runtime |

## Production Deployment

### 1. TLS Reverse Proxy
Set up nginx/caddy on a whitelisted domain:
```nginx
server {
    listen 443 ssl;
    server_name sync.yourdomain.com;
    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;
    location / {
        proxy_pass http://127.0.0.1:9274;
    }
}
```

### 2. WeChat Admin Console
- Add `sync.yourdomain.com` to request合法域名
- Configure AppID settings as needed

### 3. Upload via miniprogram-ci
```bash
cd miniprogram
npm install miniprogram-ci --save-dev
# Place private.key in ci/private.key
npm run ci:upload    # or npm run ci:preview for QR code
```

## Development

### Prerequisites
- WeChat DevTools (微信开发者工具)
- Node.js 20+
- Running SyncServer on `http://localhost:9274`

### Dev Setup
1. Open `miniprogram/` in WeChat DevTools
2. In DevTools → 详情 → 本地设置 → check "不校验合法域名"
3. Enter server address (e.g. `http://192.168.x.x:9274`) and token
4. Pull words, then review

### Running Tests
```bash
cd miniprogram
npm test              # All tests
npm run test:unit     # Unit tests only (skip E2E)
npm run test:e2e      # E2E tests (requires DevTools CLI port)
```

## Package Size

| Component | Size |
|-----------|------|
| ts-fsrs.js (vendor) | 62 KB |
| All other source | ~41 KB |
| **Total** | **~103 KB** |

Well under the 2MB main package limit. No subpackaging needed.

## Error Handling

- **Offline**: Banner shown on index page; sync buttons disabled; review still works with local data
- **Network errors**: User-friendly messages (domain whitelist, timeout, connection failure)
- **Auth errors**: 401 → "Token 无效或已过期"
- **Rate limiting**: 429 → "请求过于频繁"
- **FSRS errors**: Caught in rate(); error message shown; undo stack preserved
- **Storage quota**: `handleStorageError()` logs warning; future: LRU eviction

## Known Limitations (v1)

- No push from phone to server (review state is local-only)
- No cross-device review sync
- No add-words from phone
- No audio pronunciation
- No dark mode
- E2E tests require manual DevTools setup

## Future Enhancements

- Cross-device review sync (upload FSRS state to SyncServer)
- Add words from phone (push to SyncServer)
- Audio pronunciation (TTS or pre-recorded)
- Dark mode support
- Subpackaging if size grows
- LRU eviction for storage quota management
- GitHub Actions CI/CD
