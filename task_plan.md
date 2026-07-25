# Task Plan: WordWise WeChat Mini Program (FSRS flashcards via SyncServer)

## Goal
Build a WeChat Mini Program (in `miniprogram/`) that pulls word/content data from the existing WordWise SyncServer and implements Anki-like spaced-repetition review using the FSRS algorithm, with automated E2E tests (miniprogram-automator + jest) and automated preview/upload (miniprogram-ci).

## Current Phase
Phase 2 (Transport & Library Verification) — decisions confirmed; verifying ts-fsrs.

## Phases

### Phase 1: Requirements & Discovery  — Status: complete
- [x] Understand existing repo structure (Go/Wails desktop app + SyncServer)
- [x] Map SyncServer API endpoints + auth + data model (see findings.md)
- [x] Confirm no existing FSRS/scheduling code (FSRS is fully new)
- [x] Check tooling availability (Node v24.18.0 / npm 11.16.0)
- [x] Install miniprogram-automator + jest in `miniprogram/`
- [x] Confirm key decisions with user (answered — see Decisions Made)
- [ ] Verify ts-fsrs library fit for Mini Program runtime (find-docs) — IN PROGRESS

### Phase 2: Transport & Library Verification — Status: in_progress
- [ ] Verify ts-fsrs runs in Mini Program JS runtime + miniprogram_npm packaging (find-docs)
- [ ] Document TLS reverse proxy approach for prod (domain whitelist + https → :9274)
- [ ] Dev: confirm "skip domain check" toggle in DevTools for local http testing
- [ ] Record config template (server base URL, AppID) in findings.md
- **Note:** FSRS state is Mini-Program-LOCAL (decision A) → NO SyncServer changes. Server stays a read-only word/content source.

### Phase 3: Mini Program Scaffold + Sync Client — Status: in_progress
- [x] Create `miniprogram/` source skeleton (app.js, app.json, app.wxss, project.config.json, sitemap.json)
- [x] Index page (server/token config + test connection + pull) ✅ — list/review pages deferred to Phase 4-5
- [x] Token management (wx.setStorageSync) — user enters server addr + token
- [x] Sync client module: health, getStatus, pull (review-only → push/delete omitted for v1)
- [x] Local cache of words (wx storage; mergePulled last-write-wins by updatedAt)
- [ ] Verify in WeChat DevTools simulator

### Phase 4: FSRS Integration + Review Scheduling — Status: pending
- [ ] Add ts-fsrs (or chosen impl) as miniprogram_npm dependency
- [ ] Define review-state schema per word (due, stability, difficulty, reps, lapses, lastReview, state)
- [ ] Due-queue calculation for "today's review"
- [ ] Rating logic: Again / Hard / Good / Easy → FSRS update
- [ ] Persist + sync review state

### Phase 5: Anki-like Review UI — Status: pending
- [ ] Card front (word + phonetic) → tap to reveal back (definition/translation + LLM result)
- [ ] Rating buttons (Again/Hard/Good/Easy) with next-interval preview
- [ ] Session progress (remaining, new/learning/review counts)
- [ ] Undo last action; empty-state ("all done today")

### Phase 6: Automated Test + CI — Status: pending
- [ ] miniprogram-automator E2E: launch DevTools, navigate, assert word list + review flow
- [ ] jest config + scripts in `miniprogram/`
- [ ] miniprogram-ci script (build npm + upload + preview QR) in `miniprogram/ci/`
- [ ] (optional) GitHub Actions workflow

### Phase 7: Hardening & Delivery — Status: pending
- [ ] Error handling (offline, token invalid, network)
- [ ] Subpackage / size limits check
- [ ] Android + iOS real-device test
- [ ] Handoff docs

## Key Questions
1. **FSRS state location**: ✅ ANSWERED — **A**: review state kept Mini-Program-local (wx storage). No SyncServer changes.
2. **Production transport**: ✅ ANSWERED — TLS reverse proxy on a whitelisted domain → forward to SyncServer :9274.
3. **AppID**: ✅ ANSWERED — `wxcf0e31a667b6fe79` (real AppID provided by user).
4. **FSRS library**: ✅ ANSWERED — ts-fsrs (user-approved; verifying runtime + miniprogram_npm fit via find-docs).
5. **Scope for v1**: ✅ ANSWERED — review-only: phone pulls & studies server words; no add-words, no push.

## Decisions Made
| Decision | Rationale |
|----------|-----------|
| Mini Program lives in `miniprogram/` (separate from Wails `frontend/`) | Different build target; avoids polluting desktop frontend |
| Install miniprogram-automator + jest in `miniprogram/` (dev deps) | User-requested; standard pair for Mini Program E2E tests |
| Planning files at repo root | Effort spans `miniprogram/` + possible `syncserver/` changes |
| Use `ts-fsrs` as FSRS engine (pending verification) | Official-ish JS FSRS impl; runs in Mini Program JS runtime |
| FSRS state is Mini-Program-LOCAL (decision A) | User chose A; no server schema changes; cross-device review sync not required |
| Prod transport = TLS reverse proxy on whitelisted domain | Mini Program requires https + whitelisted domain; proxy terminates TLS → forward to :9274 |
| AppID = wxcf0e31a667b6fe79 | User-provided real AppID |
| v1 scope = review-only | Phone is a read-only consumer of SyncServer words; no push, no add-words from phone |

## Errors Encountered
| Error | Attempt | Resolution |
|-------|---------|------------|
| (none yet) | 1 | — |

## Notes
- Re-read this plan before major decisions.
- Log all errors; never repeat a failed action — mutate the approach.
- Update phase status as work progresses.
