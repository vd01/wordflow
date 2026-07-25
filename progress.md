# Progress Log: WordWise Mini Program

## Session 1 — <today>
- Read pi-planning-with-files SKILL.md; adopted file-based planning.
- Explored repo: README, go.mod, Taskfile, .gitignore, `syncserver/`, `frontend/bindings/`.
- Mapped SyncServer API + data model (see findings.md).
- Confirmed no existing FSRS/scheduling code.
- Verified tooling: Node v24.18.0, npm 11.16.0.
- Created `miniprogram/` with `package.json`.
- Installed **miniprogram-automator@0.12.1** + **jest@30.4.2** (devDeps); verified `require('miniprogram-automator')` loads (exports `launcher`, Element types, …).
- Created planning files: `task_plan.md`, `findings.md`, `progress.md` at repo root.

### Files created
- `miniprogram/package.json`
- `miniprogram/node_modules/` (miniprogram-automator, jest + deps)
- `task_plan.md`, `findings.md`, `progress.md`

### Test results
- (none yet — no Mini Program source to test)

### Next steps
- Get user answers to Key Questions #1–#5 in task_plan.md.
- Verify ts-fsrs via find-docs.
- Decide Phase 2 approach (server-extended FSRS state + HTTPS transport).

### Decisions this session
- Mini Program home = `miniprogram/` (separate from Wails `frontend/`).
- miniprogram-automator + jest installed in `miniprogram/`.

### Errors
- (none)

### TODOs / reminders
- Add `miniprogram/node_modules` (and `miniprogram/dist`, `miniprogram/miniprogram_npm` if generated) to `.gitignore` before first commit.
- Confirm actual WeChat DevTools install path for `cliPath` when writing automator tests.

## Session 2 — <today>
- User answered all 5 key questions: (1) A = local FSRS state, (2) TLS reverse proxy, (3) AppID wxcf0e31a667b6fe79, (4) ts-fsrs approved, (5) review-only.
- Verified ts-fsrs via find-docs (Context7 `/open-spaced-repetition/ts-fsrs`): API = fsrs()/createEmptyCard()/next()/repeat(); Rating Again/Hard/Good/Easy; Card fields confirmed; browser-compatible ES2020.
- Key implication: NO SyncServer changes (decision A). Mini Program is read-only consumer → pull only (no push needed).
- Started Phase 3: scaffolded Mini Program foundation (project.config.json, app.{js,json,wxss}, sitemap.json, utils/{config,sync,store}.js, pages/index).

### Decisions this session
- FSRS review state = Mini-Program-local (wx storage), keyed by entry id.
- v1 sync = pull-only (review-only scope).
- Dev: project.config.json sets `setting.urlCheck=false` to allow http SyncServer during dev.

## Session 3 — <today>
- User confirmed Phase 3 works in WeChat DevTools (index page loads, server config OK).
- Installed ts-fsrs@5.4.1 via npm; vendored `dist/index.cjs` → `utils/ts-fsrs.js` (62KB, self-contained, no require() calls).
- Created `utils/fsrs-engine.js` — FSRS wrapper: createCard, rateCard, previewIntervals, formatInterval, isDue, getDueQueue, getQueueCounts.
- Updated `utils/store.js` — added getReviews, setReviews, getReview, saveReview, removeReview, getWord, parseResult; mergePulled now also cleans up review state for deleted words.
- Created **review page** (`pages/review/`): flashcard front (word + phonetic) → tap to reveal back (definition, translation, POS, AI explanation, tags) → rating buttons (Again/Hard/Good/Easy) with interval preview → undo → session progress bar → empty state.
- Created **word list page** (`pages/wordlist/`): search, state badges (New/Learning/Review/Relearning), "Start Review" button.
- Updated `app.json` to register new pages.
- Updated index page: "单词本" → goWordList, "开始复习" → goReview, added dueCount display.

### Files created/modified
- `miniprogram/utils/ts-fsrs.js` (vendored from ts-fsrs dist)
- `miniprogram/utils/fsrs-engine.js` (new)
- `miniprogram/utils/store.js` (updated)
- `miniprogram/pages/review/review.{js,wxml,wxss,json}` (new)
- `miniprogram/pages/wordlist/wordlist.{js,wxml,wxss,json}` (new)
- `miniprogram/pages/index/index.{js,wxml}` (updated)
- `miniprogram/app.json` (updated)

### Next steps
- Test in WeChat DevTools: pull words → word list → review flow.
- Verify ts-fsrs runs correctly in Mini Program runtime.
- Phase 6: automated E2E tests.

## Session 3 — <today>
- User confirmed Phase 3 works in WeChat DevTools (index page loads, server config OK).
- Installed ts-fsrs@5.4.1 via npm; vendored `dist/index.cjs` → `utils/ts-fsrs.js` (62KB, self-contained, no require() calls).
- Created `utils/fsrs-engine.js` — FSRS wrapper: createCard, rateCard, previewIntervals, formatInterval, isDue, getDueQueue, getQueueCounts.
- Updated `utils/store.js` — added getReviews, setReviews, getReview, saveReview, removeReview, getWord, parseResult; mergePulled now also cleans up review state for deleted words.
- Created **review page** (`pages/review/`): flashcard front (word + phonetic) → tap to reveal back (definition, translation, POS, AI explanation, tags) → rating buttons (Again/Hard/Good/Easy) with interval preview → undo → session progress bar → empty state.
- Created **word list page** (`pages/wordlist/`): search, state badges (New/Learning/Review/Relearning), "Start Review" button.
- Updated `app.json` to register new pages.
- Updated index page: "单词本" → goWordList, "开始复习" → goReview, added dueCount display.
- Fixed card back content: was empty because WXML referenced wrong field names (e.g. `llmResult` instead of `definitions` array). Now correctly renders ECDICT badges, translation, definition, LLM definitions (pos+meaning+examples), memory_tips, synonyms, antonyms, etymology, exchange.
- Fixed rating buttons pinned to bottom: changed from flex layout to `position: fixed; bottom: 0` with padding-bottom on card content to avoid overlap.

### Files created/modified
- `miniprogram/utils/ts-fsrs.js` (vendored from ts-fsrs dist)
- `miniprogram/utils/fsrs-engine.js` (new)
- `miniprogram/utils/store.js` (updated)
- `miniprogram/pages/review/review.{js,wxml,wxss,json}` (new)
- `miniprogram/pages/wordlist/wordlist.{js,wxml,wxss,json}` (new)
- `miniprogram/pages/index/index.{js,wxml}` (updated)
- `miniprogram/app.json` (updated)

## Session 4 — <today>
- Phase 6: Automated testing + CI
- Created jest.config.js + __tests__/setup.js (wx global mock)
- Unit tests: store.test.js (16 tests), fsrs-engine.test.js (18 tests), sync.test.js (8 tests) — **all 42 passing**
- E2E test scaffolding: __tests__/e2e.test.js (miniprogram-automator, requires DevTools CLI port)
- CI script: ci/upload.js (miniprogram-ci preview + upload)
- npm scripts: test, test:unit, test:e2e, ci:preview, ci:upload

### Next steps
- User to verify rating buttons pinned to bottom in DevTools
- Phase 7: Hardening (error handling, offline, size limits, real-device test)
