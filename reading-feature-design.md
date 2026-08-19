# WordFlow PC — English Reading Feature Design

> Shared design decisions (grill session, 2026). Applies to the WordFlow **PC client only**.
> Original request: `need.md`. All 14 decisions below were agreed with the user.

## Overview

Add an English content reading interface to the WordFlow PC client. Three parts:
**material list (left) | reading area (center) | interactive area (right)**.
Core function: quick word lookup while reading, with the lookup result shown in
the interactive area. Users can save words to the SyncServer, see them marked in
the text, and ask questions about the material.

Reference UX: `E:/vibe-projects/read-frog` (paragraph/full/word translation styles).
Key difference from read-frog: word translation lives in the right interactive area.

## Decisions

1. **Window architecture** — a **second, dedicated window** ("reader-window"),
   opened from a header button "📖 Reading" in the main window + a tray menu item.
   Singleton: focus it if it already exists, else create lazily via `application.Get()`.
   URL `/reader.html`. Default **1280×800**, min **1000×640**.
   The reader window **really closes** on ✕ (unlike the main popup, which hides to tray).
   The app keeps running in the tray.
   **Clicking the "📖 Reading" button also hides the main popup** (frontend emits
   `hide-window` after `OpenReader()` succeeds), so only the reader window stays on
   screen; the main window returns via the global hotkey or tray "显示窗口".

2. **Material storage** — **local-only, no sync** (materials never reach the SyncServer;
   only saved words sync). New Go service **`ReadingService`** with its own SQLite DB
   (same pattern as `HistoryService`). Tables: `materials`, `material_words` (marks),
   `translations`, `chat_messages`.

3. **Text format** — **plain text only** (no Markdown). Paste → split into paragraphs
   on blank lines. Each paragraph renders as a block; every word is a clickable span.
   Size cap: **20,000 characters** (~4-5K English words), friendly error on paste if exceeded.

4. **Title** — text input + **manual "✨ Auto-generate" button** (LLM, first ~500 chars).
   No auto-generation on paste (avoids surprise LLM calls).

5. **Word lookup interaction** — **single click** on a word → lookup; **drag-select**
   across words → phrase lookup. No hover lookup, no double-click.
   Result goes **only to the right interactive area** (requirement #6).

6. **Lookup pipeline** — **instant ECDICT (`LookupWordFast`, ~0.1ms) on click**,
   compact card in the right panel; **LLM deep-dive on demand** via "✨ AI deep-dive"
   button **or `Enter` key**. Reuse the existing LRU cache (`LookupWordCached`).
   No automatic background LLM enrichment (cost + perceived slowness).
   **Compact card** (word + phonetic + 🔊 + Chinese meaning + save button + deep-dive hint),
   expandable to the full result card after deep-dive.

7. **Translation display** —
   - Paragraph translation: "译" button per paragraph (hover/right-click), result
     **inline under the paragraph** (smaller text, muted color, left border). Toggle to hide.
   - Full translation: toolbar button, one LLM call, switches reading view to
     **bilingual mode** (translation under every paragraph). Toggle back.
   - Persisted in SQLite keyed by `(material_id, paragraph_index, content_hash)`;
     edited paragraphs lose their translation via hash mismatch. Others keep theirs.

8. **Word marks** — two hierarchical states: `none → looked-up → saved`.
   - Looked-up: subtle yellow underline + brighter text.
   - Saved: green background tint + ★.
   - Stored in `material_words` (`material_id`, `word`, `status`, `updated_at`).
   - Applies to **every occurrence** of that exact word form (case-insensitive) in the material.
   - **No lemmatization** in v1 (clicking "looked" marks only "looked").
   - No accidental un-marking; saved is terminal.
   - Orphan marks (word removed by content edit) are pruned on save.

9. **Save to SyncServer** — "💾 Save to word book" button in the word card **or `Alt+S`**.
   Implementation: `HistoryService.AddHistory(word, resultJSON)` — already **upserts by
   word and auto-pushes to the SyncServer** via the sync callback. Saves whatever result
   is currently in the panel; saving again after deep-dive updates the entry with the
   richer result. After save: button shows "✓ Saved", all occurrences marked ★.
   Both `Enter` and `Alt+S` are **ignored when focus is in an input/textarea**
   (chat box, title field).

10. **Chat** — "ask questions about the material" in the interactive area.
    - Prompt layout for cache-friendliness: `system → material → history → question`.
      The material is a **stable prefix** → always hits DeepSeek's prompt cache
      (~81% of repeat lookups were cache hits per project notes).
    - History: **append-only, trimmed by token budget (~12K tokens), not by turn count**.
      Trimming drops oldest turns only when the total exceeds the budget.
      (A sliding "last N turns" window was rejected: it shifts positions every turn
      and invalidates the cached history section.)
    - Chat messages persisted per material in `chat_messages`; "clear chat" button.
    - **No streaming in v1** (non-streaming `callLLM` + "thinking…" spinner);
      SSE streaming is the v2 candidate.
    - Answer language follows the question language.
    - Binding: `ReadingService.AskQuestion(materialID, question) → string`.

11. **Frontend structure** — separate multi-page build:
    `frontend/reader.html` + `frontend/src/reader.ts` + `frontend/src/reader.css`;
    Vite config adds `reader.html` as a second rollup input. `main.ts` stays untouched.
    Shared `style.css` for theme variables/common components.
    Bindings: reuse `frontend/bindings/wordflow/dictservice.ts` as-is; new
    `ReadingService` bindings generated or hand-patched with FNV-1a IDs (per AGENTS.md).

12. **Material list** — items show title, word count, created date, ★ saved-word count.
    Sorted by most recently updated first. Actions: **Open, Edit, Delete (confirm)**.
    **Content editing IS in v1** via the same paste form, pre-filled. Delete = hard delete
    with cascade (marks, translations, chat). Empty state guide text; paste form opens
    automatically on first run. No file import, no folders/tags (v2).

13. **LLM config** — all reading features (deep-dive, paragraph/full translation,
    auto-title, chat) use the **same LLM config** as the dictionary
    (API key / URL / model / proxy from Settings). Fixed system prompts for
    translation/chat (not the user-editable PromptConfig). Per-feature model = v2.

14. **Build order & testing** —
    - Phase 1: `ReadingService` skeleton (materials CRUD) + `OpenReader()` window +
      3-part layout + paste form + material list.
    - Phase 2: reading rendering, clickable words, click → `LookupWordFast`,
      compact card, looked-up marks, mark pruning on edit.
    - Phase 3: save (`Alt+S`/button) + `Enter` deep-dive.
    - Phase 4: paragraph + full translation (hash-keyed persistence).
    - Phase 5: chat.
    - Tests: **Go unit tests** for `ReadingService` logic (mark pruning, hash
      invalidation, chat trimming) + manual frontend checklist. No frontend test
      infrastructure in v1.

## Implementation notes (from codebase exploration)

- `main.go` embeds `all:frontend/dist` → a Vite multi-page build is picked up automatically.
- Save-to-server needs **zero new sync code**: `HistoryService.addHistoryInternal`
  already upserts by lowercase word and fires `syncCb` → `SyncService.OnEntryAdded`.
- History words are stored lowercase; reading-view saves should normalize the same way
  (handled by `AddHistory` itself).
- `DictService` bindings are method-name hashed (FNV-1a); do NOT regenerate all bindings —
  hand-patch or generate only the new `ReadingService` file.
- The global hotkey (Ctrl+Alt+Q) keeps working while the reader window is focused
  (UIA reads foreground selection) — free synergy, no extra work.
- Saved result JSON should be the standard merged format (same shape the Android app
  parses). Enriching the saved result with the material sentence = v2 idea.

## Explicitly out of scope (v2 ideas)

Material sync, Markdown, hover lookups, lemmatization, streaming chat, per-feature
models, file import, folders/tags, sentence-in-save enrichment.

---

## Implementation status (2026-08): ALL PHASES IMPLEMENTED

Phases 1-5 are built: `reading_service.go` (+tests), second window via `OpenReader()`,
`reader.html`/`reader.ts`/`public/reader.css`, Vite multi-page, hand-written bindings
(`frontend/bindings/wordflow/readingservice.ts`, FNV-1a IDs), "📚 Reading" header
button + tray menu item, `callLLM` → `callLLMMessages` refactor.

**Deviations from the agreed design (deliberate, behavior-equivalent):**

1. **Translation cache key**: content_hash → `(material_id, paragraph_index)`.
   `UpdateMaterial` compares old/new paragraph texts by index and deletes
   translations for changed/removed paragraphs. Same user-visible behavior
   (stale translations disappear on edit, unchanged ones survive), simpler code,
   and the frontend doesn't need a sha256 implementation.
2. **Full translation** is driven by the frontend: it loops `TranslateParagraph`
   per paragraph (progress shown on the button), instead of one backend call.
   One LLM call per paragraph with per-paragraph caching — more robust for
   long texts, free progress UI, and partial results survive failures.
3. **Material list** does not ship `content`: added a `word_count` column to
   `materials` (migration included) so `GetMaterials` stays light.
4. **Chat trimming** drops whole oldest user+assistant pairs (not single
   messages) so the cache rebuilds once; a lone over-budget message is dropped
   only as a last resort.

**Testing**: `reading_service_test.go` — 9 tests. NOTE: run `go test -vet=off .`
(plain `go test` fails on pre-existing vet errors in main.go SyncService).
