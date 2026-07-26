# Task Plan: WordFlow WeChat Mini Program (FSRS flashcards via SyncServer)

## Goal
Build a WeChat Mini Program (in `miniprogram/`) that pulls word/content data from the existing WordFlow SyncServer and implements Anki-like spaced-repetition review using the FSRS algorithm, with automated E2E tests (miniprogram-automator + jest) and automated preview/upload (miniprogram-ci).

## Current Phase
All phases complete. Remaining: real-device test + production TLS setup (user action).

## Phases

### Phase 1: Requirements & Discovery — ✅ Complete
- [x] Understand existing repo structure (Go/Wails desktop app + SyncServer)
- [x] Map SyncServer API endpoints + auth + data model (see findings.md)
- [x] Confirm no existing FSRS/scheduling code (FSRS is fully new)
- [x] Check tooling availability (Node v24.18.0 / npm 11.16.0)
- [x] Install miniprogram-automator + jest in `miniprogram/`
- [x] Confirm key decisions with user (all 5 answered)

### Phase 2: Transport & Library Verification — ✅ Complete
- [x] Verify ts-fsrs runs in Mini Program JS runtime (vendored CJS bundle, 62KB, self-contained)
- [x] Document TLS reverse proxy approach for prod (domain whitelist + https → :9274)
- [x] Dev: project.config.json sets setting.urlCheck=false for local http testing
- [x] Default server URL: https://vocab-agent.duckdns.org:31588/

### Phase 3: Mini Program Scaffold + Sync Client — ✅ Complete
- [x] Create `miniprogram/` source skeleton
- [x] Index page (server/token config + test connection + pull)
- [x] Token management (wx.setStorageSync)
- [x] Sync client module: health, getStatus, pull
- [x] Local cache of words (wx storage; mergePulled last-write-wins by updatedAt)
- [x] Verified in WeChat DevTools

### Phase 4: FSRS Integration + Review Scheduling — ✅ Complete
- [x] Add ts-fsrs as dependency; vendored CJS bundle into utils/ts-fsrs.js
- [x] Define review-state schema per word
- [x] Due-queue calculation (getDueQueue)
- [x] Rating logic: Again/Hard/Good/Easy → FSRS update (rateCard)
- [x] Persist review state (store.saveReview / getReview)
- [x] Verified in DevTools

### Phase 5: Anki-like Review UI — ✅ Complete
- [x] Card front (word + phonetic) → tap to reveal back (definition/translation + LLM result)
- [x] Rating buttons (Again/Hard/Good/Easy) with next-interval preview, pinned to bottom
- [x] Session progress (remaining, new/learning/review counts)
- [x] Undo last action; session-complete screen with stats
- [x] Word list page with search + state badges + clickable detail view
- [x] Word detail page (full content + FSRS state info)
- [x] Auto-launch to review when configured + due cards exist
- [x] Settings button on review page

### Phase 6: Automated Test + CI — ✅ Complete
- [x] jest config + wx mock setup (jest.config.js, __tests__/setup.js)
- [x] Unit tests: store.js (16), fsrs-engine.js (18), sync.js (8) — all 42 passing
- [x] E2E test scaffolding (miniprogram-automator, requires DevTools CLI port)
- [x] miniprogram-ci script (ci/upload.js — preview + upload)
- [x] npm scripts: test, test:unit, test:e2e, ci:preview, ci:upload

### Phase 7: Hardening & Delivery — ✅ Complete
- [x] Error handling (offline banner, network errors, auth errors, rate limiting, FSRS errors, storage quota)
- [x] Auto sync on launch (full pull if never synced, delta otherwise)
- [x] Sync promise shared with pages — UI updates after sync completes
- [x] Network reconnect triggers re-sync
- [x] Package size ~103KB, well under 2MB limit
- [x] Review page: validate rating, deep-copy undo, refresh counts on session end
- [x] Formatted "上次同步" display (relative time + absolute date)
- [x] Handoff docs (miniprogram/HANDOFF.md)
- [ ] Android + iOS real-device test (requires physical device)
- [ ] Production TLS reverse proxy setup (requires domain + cert)

## Decisions Made
| Decision | Rationale |
|----------|-----------|
| Mini Program lives in `miniprogram/` (separate from Wails `frontend/`) | Different build target; avoids polluting desktop frontend |
| Install miniprogram-automator + jest in `miniprogram/` (dev deps) | User-requested; standard pair for Mini Program E2E tests |
| Use `ts-fsrs` as FSRS engine (vendored CJS) | Self-contained, no require() calls, browser-compatible |
| FSRS state is Mini-Program-LOCAL (decision A) | No server schema changes; cross-device sync not required for v1 |
| Prod transport = TLS reverse proxy on whitelisted domain | Mini Program requires https + whitelisted domain |
| AppID = wxcf0e31a667b6fe79 | User-provided real AppID |
| v1 scope = review-only | Phone is a read-only consumer of SyncServer words |
| Default server = https://vocab-agent.duckdns.org:31588/ | User-provided production server URL |
| Auto-launch to review when due cards exist | Better UX — skip settings page on return visits |
