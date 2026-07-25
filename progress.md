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
