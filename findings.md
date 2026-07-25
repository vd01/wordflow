# Findings: WordWise Mini Program

## Repo structure (E:/go-wails-wordwise)
- Go + Wails v3 desktop app ("wordwise"). `main.go`, `frontend/` (Vite+TS desktop UI), `go.mod` (module `wordwise`, go 1.25).
- `syncserver/` — standalone HTTP sync server (Go, SQLite via modernc.org/sqlite).
- `cmd/syncserver/main.go` — server entrypoint binary. Default port **9274**, default DB `~/.../WordWise/sync.db`.
- `frontend/bindings/wordwise/*.ts` — auto-generated Wails bindings (dictservice, ecdictservice, historyservice, syncservice, models).
- Build tooling: `Taskfile.yml` (go-task), `wails3 dev/build`. Server mode: `task build:server` / `run:server`.
- `.gitignore` ignores `.task`, `frontend/dist`, `frontend/node_modules`. **Note:** does NOT ignore `miniprogram/node_modules` — add later.

## Tooling available
- Node **v24.18.0**, npm **11.16.0**.
- `miniprogram/` created with `package.json`; installed **miniprogram-automator@0.12.1** + **jest@30.4.2** as devDeps. `require('miniprogram-automator')` loads OK; key export is `launcher`.
- npm 11 printed allow-scripts warnings (postinstall of unrs-resolver, core-js) — informational only, not blocking.

## SyncServer API (from syncserver/server.go + store.go)
Base: `http://<host>:9274`. Auth: token in `Authorization: Bearer <token>` OR `X-Sync-Token`. CORS `*`. Read/Write/Idle timeouts 30/60/120s. Body limit 10MB, max 1000 entries per push.

| Method | Path | Auth | Notes |
|--------|------|------|-------|
| POST | `/api/v1/user/create` | public | → `{token, message, createdAt}` |
| GET  | `/api/v1/user/status` | yes | → `{token, wordCount, lastSync, createdAt}` |
| POST | `/api/v1/sync/push` | yes | body `{entries:[...]}`; upsert, last-write-wins by `updatedAt` |
| GET  | `/api/v1/sync/pull?since=<unix>` | yes | `since=0` → all non-deleted; else delta. → `{entries:[...], serverNow}` |
| POST | `/api/v1/sync/delete` | yes | body `{id}`; soft-delete |
| GET  | `/api/v1/health` | public | → `{status, service, version, time}` |

### Data model — SyncEntry (store.go)
```
{
  id: string,            // unique (nanosecond ts on desktop)
  word: string,          // the looked-up word
  result: string,        // JSON string of merged ECDICT + LLM result
  createdAt: int64,      // unix seconds
  updatedAt: int64,      // unix seconds (used for sync conflict resolution)
  deleted: bool          // soft delete
}
```
DB table `sync_entries(id, token, word, result, created_at, updated_at, deleted)`, PK `(id, token)`. Indexes on `(token, updated_at)` and `(token, word)`.

Desktop `HistoryEntry` (models.ts) = same shape minus `deleted` (`id, word, result, createdAt, updatedAt`). The `result` JSON contains merged ECDICT entry (`EcdictEntry`: word, phonetic, definition, translation, pos, collins, oxford, tag, bnc, frq, exchange) + LLM-generated content.

## FSRS / scheduling — current state
- **No FSRS/Anki/spaced-repetition code exists** anywhere in repo. (The only `schedule*` matches in `frontend/src/main.ts` are a UI debounce `schedulePreview`, unrelated.)
- ⇒ FSRS engine + review-state storage are entirely new work.

## Critical WeChat constraint: HTTPS + domain whitelist
- Mini Program `wx.request` ONLY allows **https** URLs whose domain is whitelisted in the WeChat admin console (request合法域名), one per type, max 200.
- SyncServer is **http on port 9274** → not callable from a production Mini Program.
- Dev workaround: in DevTools → 详情 → 本地设置 → check "不校验合法域名、web-view…、TLS 版本…". (Also in real-device dev this toggle can be enabled.)
- Production options:
  1. Reverse proxy (nginx/caddy) terminating TLS on a whitelisted domain → forward to `:9274`.
  2. WeChat Cloud Function (云函数) as a proxy to the SyncServer (also handles domain issue).
  3. Migrate sync data to WeChat Cloud DB (云数据库) — bigger change.

## miniprogram-automator setup notes
- Requires WeChat DevTools with **CLI/HTTP port enabled** (设置 → 安全设置 → 服务端口).
- Windows DevTools CLI path (typical): `C:\Program Files (x86)\Tencent\微信web开发者工具\cli.bat` — verify actual install path.
- Requires base library ≥ 2.7.3, DevTools ≥ 1.02.1907232.
- Usage: `automator.launch({ cliPath, projectPath })` then `miniProgram.currentPage()`, `page.$('.cls')`, element taps, `miniProgram.callWxMethod(...)`, etc.
- Real-device automation supported via remote debugging (same base library).

## FSRS engine — VERIFIED (ts-fsrs via find-docs/Context7)
- Library: `ts-fsrs` (Context7 ID `/open-spaced-repetition/ts-fsrs`; High reputation; 466 snippets). v5.4.1+ = FSRS-6.
- Browser-compatible ES2020 (example.html uses CDN ESM import); ships `dist/index.mjs` (ESM). Node 20+ needed only to BUILD; runtime is browser-safe → OK for Mini Program JS engine.
- **Core API**: `import { fsrs, Rating, createEmptyCard } from 'ts-fsrs'`
  - `const scheduler = fsrs({ requestRetention, maximumInterval, w })` (params optional)
  - `createEmptyCard()` → new card
  - `scheduler.next(card, now, Rating.X)` → `{ card, log }` (apply a rating)
  - `scheduler.repeat(card, now)` → object keyed by Rating → use to PREVIEW next intervals on the 4 buttons
- **Rating enum**: Manual=0, Again=1, Hard=2, Good=3, Easy=4
- **Card fields**: `due, stability, difficulty, elapsed_days, scheduled_days, reps, lapses, state, last_review` (state ∈ New/Learning/Review/Relearning)
- **Packaging risk**: bare `import 'ts-fsrs'` won't run in Mini Program → use `miniprogram_npm` (DevTools "build npm") or vendor a CJS bundle into `utils/`. Plan: add ts-fsrs to `miniprogram/package.json` deps, try build-npm; fallback = vendor `dist/index.mjs`/CJS into `miniprogram/utils/fsrs.js`. Handle in Phase 4.

## Open questions captured in task_plan.md → Key Questions
All 5 answered (see task_plan.md → Decisions Made). Decisions: A (local FSRS), TLS reverse proxy, AppID wxcf0e31a667b6fe79, ts-fsrs, review-only.
