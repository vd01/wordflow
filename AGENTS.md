# WordFlow — Project Knowledge Base

> This file records hard-won discoveries about the project so that any agent (or human) can get productive immediately without re-discovering the same things.

---

## Project Overview

WordFlow is a **vocabulary learning app** with three frontends sharing a Go sync server:

| Frontend | Stack | Location |
|----------|-------|----------|
| **Desktop** | Wails v3 (Go backend + HTML/TS frontend) | `main.go`, `frontend/` |
| **Android** | Kotlin + Jetpack Compose | `android/app/src/main/java/com/wordflow/android/` |
| **Mini Program** | WeChat Mini Program (JS) | `miniprogram/` |
| **Sync Server** | Go (HTTP + SQLite) | `syncserver/`, `cmd/syncserver/` |

All three frontends implement the **FSRS-6 spaced repetition algorithm** independently:
- Mini Program → `miniprogram/utils/ts-fsrs.js` (vendored ts-fsrs CJS bundle) + `fsrs-engine.js` (wrapper)
- Android → `android/.../data/FsrsEngine.kt` (hand-ported Kotlin, 21-weight FSRS-6)
- Desktop → no local FSRS; reviews happen on mobile/mini-program

---

## Build & Run

### Android
```bash
# JDK 17+ required (JDK 8 won't work with AGP 8.2.2)
export JAVA_HOME="/c/Program Files/OpenJDK/jdk-20.0.1"
cd android
./gradlew assembleDebug          # build debug APK
./gradlew installDebug           # build + install to connected device
# APK output: android/app/build/outputs/apk/debug/app-debug.apk
```

**SDK location**: `Q:/Android` (set in `android/local.properties` as `sdk.dir=Q:/Android`)

**Proxy**: Clash runs on port `7993`. If `git push` fails, set:
```bash
git config http.proxy http://127.0.0.1:7993
git config https.proxy http://127.0.0.1:7993
```

**Signature mismatch**: If `installDebug` fails with `INSTALL_FAILED_UPDATE_INCOMPATIBLE`, uninstall first:
```bash
adb uninstall com.wordflow.android
```

### Desktop (Wails)
```bash
wails3 dev          # run with hot-reload
```

### Mini Program
```bash
cd miniprogram && npm test   # runs __tests__/ via miniprogram-simulate
```

---

## FSRS Algorithm — Critical Knowledge

### The Kotlin port had a NaN crash (fixed 2025-08)

The Android `FsrsEngine.kt` crashed with `Cannot round NaN value` when rating cards that had `stability = 0.0` (corrupted or uninitialized state).

**Root cause**: `forgettingCurve()` divides by `stability` without a guard → `Infinity` → propagates through calculations → `NaN` → `round(NaN)` throws.

**Fix applied**:
1. `forgettingCurve()`: Added `if (stability <= 0.0) return 0.0` guard
2. `roundTo()`: Added `if (v.isNaN() || v.isInfinite()) return 0.0` guard as a safety net

**Key lesson**: The **ts-fsrs.js** in `miniprogram/utils/` is the **source of truth** for the FSRS algorithm. Any port (Kotlin, Swift, etc.) must match it formula-by-formula. When in doubt, diff against `ts-fsrs.js`.

### Current Android FsrsEngine (21-weight FSRS-6)

The Android app uses the full FSRS-6 with 21 weights, learning steps, short-term stability, and a unified `nextState()` method. Key differences from the mini program's simpler 17-weight version:

- **21 weights** (w[0]..w[20]), including w[17]..w[20] for short-term stability and decay
- **Learning steps**: `["1m", "10m"]` for new cards, `["10m"]` for relearning
- **Short-term stability**: `nextShortTermStability()` used when `t=0` (same-day review)
- **`nextState()`**: Unified method that handles all rating paths (new, short-term, forget, recall)
- **`applyLearningSteps()`**: Handles minute-based scheduling for learning/relearning states
- **`learningSteps` field** on `FsrsCard`: tracks current step index within the learning steps

### FSRS formula reference (from ts-fsrs)

```
Forgetting curve:  R(t,S) = (1 + FACTOR * t / (9*S))^DECAY
                    DECAY = -0.5 (17-weight) or -w[20] (21-weight FSRS-6)
                    FACTOR = 19/81 (17-weight) or derived from decay (21-weight)

Init stability:    S0(G) = w[G-1]           (G=1..4)

Init difficulty:   D0(G) = w4 - exp((G-1)*w5) + 1,  clamped [1,10]

Next difficulty:   delta_d = -w6*(G-3)
                   next_d = D + delta_d*(10-D)/9     (linear damping)
                   D' = w7*D0(Easy) + (1-w7)*next_d  (mean reversion), clamped [1,10]

Recall stability:  S'_r = S * (1 + exp(w8)*(11-D)*S^(-w9)*(exp(w10*(1-R))-1)*hard_penalty*easy_bonus)
                   hard_penalty = w15 if G=Hard else 1
                   easy_bonus   = w16 if G=Easy  else 1
                   clamped [0.001, 36500]

Forget stability:  S'_f = w11 * D^(-w12) * ((S+1)^w13 - 1) * exp(w14*(1-R))
                   clamped [0.001, 36500]

Short-term (FSRS-6): S'_s = S * S^(-w19) * exp(w17*(G-3+w18))
                   if G>=Hard: max(sinc, 1), else sinc
                   clamped [0.001, 36500]

Interval:          intervalModifier = (requestRetention^(1/decay) - 1) / factor
                   I = min(max(1, round(S * intervalModifier)), maximumInterval)
```

---

## Architecture Notes

### Data flow
- Desktop app looks up words (ECDICT + LLM) → saves `SyncEntry` with `result` JSON
- All clients sync via the Go server (`SyncService.pushEntry` / `pullEntries`)
- Android/Mini Program store reviews locally (`FsrsCard` per word)
- Reviews are synced to server (Android version syncs review state)

### Store (Android)
- Uses `SharedPreferences` + Gson for all persistence (words, reviews, config)
- `Store.kt` handles: words, reviews, daily limits, sync state
- Gson can bypass Kotlin null-safety — `parseResult()` explicitly coalesces null fields to `""`

### Store (Mini Program)
- Uses WeChat `wx.getStorageSync` / `setStorageSync`
- `store.js` mirrors Android `Store.kt` functionality

---

## Common Pitfalls

1. **Gson null-safety bypass**: Kotlin data class fields declared non-null can receive null via Gson deserialization. Always coalesce with `?: ""` after `gson.fromJson()`.

2. **FSRS port drift**: Any time the FSRS algorithm is touched, verify against `miniprogram/utils/ts-fsrs.js`. The JS implementation is the canonical reference.

3. **JDK version**: Android build requires JDK 17+. System default may be JDK 8. Always set `JAVA_HOME` before building.

4. **Division by zero in FSRS**: `forgettingCurve()` and `s.pow(-w[9])` can produce Infinity/NaN when stability is 0. Always guard with `if (stability <= 0) return 0.0`.

5. **Snake_case vs camelCase in JSON**: The desktop Go app produces `snake_case` keys (e.g. `memory_tips`, `english_example`), while Gson expects `camelCase`. `Store.parseResult()` has a fallback manual mapping for snake_case keys.

6. **Git proxy**: Network access to GitHub requires Clash proxy on port 7993. Set git config or env vars before pushing.

---

## ECDICT Query Performance (fixed 2025-08)

**Symptom**: ECDICT lookups on the PC client were extremely slow *sometimes* (hundreds of ms instead of ~100ms).

**Root cause**: `WHERE word = ? COLLATE NOCASE` forces a **full table scan** of the 770K-row / 101MB `ecdict` table (~30-80ms per query, worse with cold OS page cache). This was the fallback in `EcdictService.LookupEcdict` step 3, hit whenever a word wasn't found by exact or lowercase match (misspellings, proper nouns, unknown words). Two additional issues:

1. `PRAGMA cache_size=-8192` (8MB) and `mmap_size=33554432` (32MB) covered only ~7% / ~32% of the 101MB DB → every lookup after idle time re-read pages from disk.
2. `HistoryService.GetHistoryByWord` and `addHistoryInternal` also used `COLLATE NOCASE` (full scan), though history is small.

**Fix applied**:
1. `LookupEcdict`: replaced unconditional COLLATE NOCASE with an **indexed title-case lookup** (`"Bahai"` from `"bahai"`) — same coverage for the ~81K proper nouns, ~0.1ms instead of ~30-80ms. COLLATE NOCASE now runs **only for multi-word phrases** (rare edge case like `"A AND NOT B gate"`).
2. `openDB` pragmas: `cache_size=-65536` (64MB), `mmap_size=134217728` (128MB — covers the full DB).
3. History queries: normalize input to lowercase + exact match (uses `idx_history_word` index).

**Benchmark result** (modernc.org/sqlite, 770K rows): unknown-word worst case went from **29.2ms → 0.2ms avg** (~146x).

**Key lesson**: `COLLATE NOCASE` in SQLite disables index usage → full table scan. For case-insensitive lookup on a big table, prefer normalizing the input (lowercase/title-case) and using exact matches against indexed columns. The `idx_ecdict_word` index is redundant (word is PRIMARY KEY → `sqlite_autoindex_ecdict_1` covers everything) and kept only because the fuzzy prefix query uses it as a covering index.

---

## LLM Query Performance (reviewed 2025-08)
---

## UIA Selection Reading for the Hotkey (added 2025-08)

**Feature**: The global hotkey now reads the *foreground window's selected text* via UI Automation (`uia_windows.go`) before falling back to the clipboard. In browsers/editors the flow becomes: select word → hotkey (no Ctrl+C).

**Verified vtable layout** (empirically, on Windows 10 19045, via the probe program at `E:/uia_probe`):
- `IUIAutomation.GetFocusedElement` = vtable **8** (IUnknown 0..2 + own index 5)
- `IUIAutomationElement.GetCurrentPattern` = vtable **14 or 16** — the slot is OS-dependent (old SDKs put `GetCurrentPattern` at 14, newer at 16). Probe BOTH slots and use whichever writes the 2nd argument register (the 2-arg method).
- `TextPattern.GetSelection` = vtable **5**, `TextRangeArray.Length/GetElement` = **3/4**, `TextRange.GetText` = **12**
- `UIA_TextPatternId` = 10014; `CLSID_CUIAutomation` = `ff48dba4-60ef-4201-aa87-54103eef594e`; `IID_IUIAutomation` = `30cbe57d-d9d0-452a-ab13-7ac5ac4825ee`

**Critical pitfall — GetCurrentPatternAs is broken on Win10**: the As-variant QueryInterfaces for `IID_IUIAutomationTextPattern`, whose GUID in the Win11 SDK (10.0.22621: `32eba289-3583-42c9-9c59-3b6d9a1e9b6a`) is NOT recognized by the Win10 system `UIAutomationCore.dll` → `E_NOINTERFACE`. **Use the 2-arg `GetCurrentPattern`** — it returns the raw pattern object whose own vtable IS the TextPattern vtable, so it works on both layouts.

**Other findings**:
- Classic Notepad `Edit` control does NOT expose TextPattern; conhost/Windows Terminal and Chromium (Chrome/Edge web content + address bar) DO. Chromium exposes web paragraphs as named elements with the pattern.
- `CoInitializeEx(0, COINIT_APARTMENT_THREADED)` on a fresh goroutine + synchronous outbound COM calls need no message pump. Run the query in a goroutine with a ~1.2s timeout so a hung foreground app can't freeze the app; fall back to clipboard on timeout/failure.
- Order matters: query UIA BEFORE `w.Show()`/`w.Focus()`, or `GetFocusedElement` returns WordFlow's own webview.
- `golang.org/x/sys/windows` does NOT export `SendInput`/`keybd_event`/`INPUT`; wails `pkg/w32.SendInput` is CGo-based with a nonstandard struct layout. If auto-copy is ever added, define a custom `INPUT` struct (32 bytes on x64) and call `user32.SendInput` via `syscall.NewLazyDLL`.

**Measured baseline** (DeepSeek `api.deepseek.com`, model `deepseek-v4-flash`, from CN network):
- DNS+TCP+TLS+TTFT: **~85-217ms** — fast, not the bottleneck
- Generation rate: **~95 tokens/sec** (measured: 85-111 tok/s across 8 words)
- **Average output length: ~355 tokens** (measured range 233-537, n=8) → **~3.8s total latency** per lookup
- Prompt: ~314 tokens, of which **~256 tokens (81%) served from DeepSeek prompt cache** on repeat lookups
- All responses finish with `finish_reason=stop` — the live `maxTokens=3000` cap is never hit

**Conclusion**: LLM "slowness" is **generation-bound** (output token count × ~85 tok/s), not network or client code. TTFT is already fast.

**Fixes applied (main.go)**:
1. `DictService.httpClient`: `Timeout 30s→60s` (a legitimate 2000-token response takes ~24s; 30s caused hard failures), `IdleConnTimeout 30s→2min`, `MaxIdleConns 2→8`, `MaxIdleConnsPerHost 2→4` (lookups are sporadic → old 30s idle timeout killed keep-alive, forcing a DNS+TCP+TLS handshake every lookup).
2. `fetchFreeDictAudio`: was creating a brand-new `http.Client{Timeout:5s}` per call (no pooling) → now reuses `d.httpClient` with a 5s context timeout.

**Remaining levers (not implemented)**:
- **Streaming (SSE)**: biggest perceived-speed win — TTFT is only ~100ms, so streaming would show content almost instantly and fill progressively. Requires `stream:true` + SSE parsing in Go + incremental render in frontend.
- **Reduce output size**: trim fields/descriptions in prompt config, or cap `max_tokens` below 2000 (risk: JSON truncation → app falls back to ECDICT-only).
- **Send ECDICT context to LLM**: frontend uses `LookupWordLLMFast` (includeEcdict=false) so the LLM never sees ECDICT data and writes a full standalone definition. Since the ECDICT query is now ~0.1ms, switching to `LookupWordLLM` (includeEcdict=true) adds negligible cost and may improve quality + slightly shorter outputs ("避免重复已有信息").

---

## HTTP Proxy Support (added 2025-08)
---

## WebView2 Renderer Throttling — hotkey events can be lost (found 2025-08)

**Symptom**: after the global hotkey, the lookup sometimes starts 1-9s late, or never (the emit was sent but no search ever hit the backend). Timing was inconsistent (0s/1s/4s/9s gaps).

**Root cause**: WebView2 throttles/freezes the renderer while the window is hidden ("efficiency mode", see wails `webview_window_windows.go` `show()`/`hide()` and issue #2861). The wails code deliberately keeps the controller `IsVisible=true` to avoid efficiency mode, but the page timers and event delivery still get throttled when the OS window is hidden — so a one-shot `app.Event.Emit` raced with the thaw and was delayed or dropped.

**Fix (pull model)**: `DictService.pendingWord` + `GetPendingWord()` binding (FNV-1a method ID; hand-patched `frontend/bindings/wordflow/dictservice.ts` — the ID for `main.DictService.<Method>` can be computed with `hash/fnv` fnv32a). The frontend polls `GetPendingWord()` every 300ms and searches whatever it returns; the event is kept as a fast path. A pending word survives freeze/thaw and can't be lost.

**Also fixed**: `doSearch` in `main.ts` silently DROPPED a word when another search was in flight (`if (isSearching) { ...; return; }`). Now it queues the word (`queuedWord`) and the in-flight search hands over via `startQueuedSearch()` at its stale checkpoints.

**Lesson**: with Wails v3 + WebView2, do not rely on one-shot event delivery to the frontend around window hide/show — prefer pull/poll patterns for anything that must not be lost. Bindings are method-name hashed (`fnv32a("main.DictService.MethodName")`); compute IDs and hand-patch the TS file instead of full regeneration.

**Feature**: PC client now routes LLM + audio requests through an HTTP proxy, defaulting to `http://127.0.0.1:7993` (Clash).

**Config**: `proxy` key in `config.json` (editable in Settings → LLM tab → "HTTP Proxy").
- Key **absent** → default `http://127.0.0.1:7993` (first run)
- Key **present but empty** → direct connection (no proxy)
- Key present → use that proxy URL

**Implementation** (`main.go`):
- `DictService.proxy` field; loaded in `ServiceStartup` before building `httpClient`.
- `proxyFallbackTransport`: tries the proxy first; on connection error **or** proxy-level 502/503/504 it retries once **direct** (clones the request, rewinds body via `GetBody`). This keeps the app working when Clash is off or unrouted.
- `SaveConfig(apiKey, apiURL, modelName, shortcutKey, proxy)` — 5th param added; binding ID unchanged (wails IDs are method-name based, so the committed `frontend/bindings/wordflow/dictservice.ts` was hand-patched instead of full regeneration — regeneration would rewrite all bindings to a newer style).
- `GetConfig()` returns `proxy`.

**Verified**: dead proxy → direct fallback works (0ms); Clash 502 → direct fallback works (162ms); real DeepSeek call via Clash → 200 in 622ms.

**Note**: sync service uses a separate HTTP/3 client (QUIC) — proxy does NOT apply to sync (out of scope).

**Gotcha — Groq 403 Forbidden**: Groq only serves US/UK IPs and returns `403 {"error":{"message":"Forbidden"}}` for other regions (e.g. direct connections from CN). The app must reach Groq through a US/UK proxy node. Also note:
- Groq model names are like `llama-3.3-70b-versatile`; the `openai/gpt-oss-20b` style IS valid on Groq (OpenAI-hosted models), but verify the exact name in the Groq console.
- Groq accepts `max_tokens` (aliased), so no request-shape change was needed.
- If you still get 403 with the proxy enabled, the Clash node's exit region isn't US/UK (check by curling `https://api.groq.com/...` through the proxy).

**Ordering lesson (bug fixed 2025-08)**: in `DictService.ServiceStartup`, config (including `proxy`) MUST be loaded BEFORE building `httpClient` — the transport captures `d.proxy` at construction time. The first version of the proxy feature loaded config after building the client, so the app always connected directly (which is why switching to Groq produced 403).

## English Reading Feature (added 2026-08)

Full design: `reading-feature-design.md`. Implements `need.md`. **PC client only.**

**Architecture**:
- Second **dedicated window** ("reader-window", 1280×800, min 1000×640) opened via `ReadingService.OpenReader()` (singleton — focus if exists, else `application.Get().Window.NewWithOptions` with `URL: "/reader.html"`). Window really closes on ✕ (unlike the main popup).
- `ReadingService` (`reading_service.go`): **local-only SQLite** at `%UserConfigDir%/WordWise/reading.db` (NOT synced — only saved words sync via the existing HistoryService path). Tables: `materials` (+`word_count` column), `material_words`, `translations`, `chat_messages`.
- **Bindings are method-name hashed (fnv32a of `main.ReadingService.<Method>`)** — `frontend/bindings/wordflow/readingservice.ts` was hand-written with computed IDs (bindings/ is gitignored). Never regenerate all bindings.
- Frontend is a **Vite multi-page build**: `reader.html` + `src/reader.ts` + `public/reader.css` (CSS must live in `public/` like `style.css` — `/reader.css` is referenced at runtime). `vite.config.ts` adds `reader.html` as a rollup input.

**Key behaviors**:
- Paragraph splitting must mirror Go exactly: `splitParagraphs` in `reading_service.go` == `splitParagraphs` in `reader.ts` (join hard-wrapped lines on single newlines; blank lines separate paragraphs). Changing one without the other breaks translation indexing.
- Word lookup pipeline: click/drag-select → `LookupWordFast` (ECDICT ~0.1ms) → compact card; `Enter` or "✨ AI 深入" → `LookupWordLLMFast` → merge (mirrors `mergeResults` in main.ts). `Alt+S` or button → `HistoryService.AddHistory(word, resultJSON)` (upserts + auto-syncs) + `SetMark(status=2)`.
- Marks: `material_words(status)` — 1 = looked-up (yellow underline), 2 = saved (green ★). Saved wins; escalation only (never downgrade). Marks apply to every occurrence of the exact word form; no lemmatization.
- Translations: cached per `(material_id, paragraph_index)` — **index-based invalidation** (deviation from the design doc's hash idea): `UpdateMaterial` compares old/new paragraph texts by index and deletes changed/removed indexes' translations. Full translation = frontend loops `TranslateParagraph` per paragraph (progress on button).
- Chat: `AskQuestion` builds `system(chatSystemPrompt + material)` → history → question. **Append-only history, trimmed by token budget (~12K tokens) not turn count** — keeps DeepSeek's prefix cache hitting (material is a stable prefix). `trimChatHistory` drops whole oldest user+assistant pairs. Q&A persisted only on success.
- Shortcut guards: `Alt+S`/`Enter` are ignored while typing in INPUT/TEXTAREA; Enter is never stolen from a focused BUTTON. Chat send = Ctrl+Enter in the textarea.
- `DictService.callLLM` refactored: now delegates to `callLLMMessages(messages, ...)` (multi-turn support). Config guard (ready + apiKey/apiURL/model) moved into `callLLMMessages` so ALL LLM callers are protected, including reading-service calls.

**Tests**: `reading_service_test.go` (`go test -vet=off .` — plain `go test` fails on pre-existing vet errors in `main.go` SyncService lines ~2886/2929/2986, not ours). Covers: paragraph splitting, mark escalation, orphan-mark pruning, translation invalidation on edit, cascade delete, chat trimming, char limit.
