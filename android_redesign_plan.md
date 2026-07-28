# WordFlow Android — UI Redesign Plan

> Status: **Proposal / plan only** — no code changed yet.
> Scope: the native Android app in `android/app/src/main/java/com/wordflow/android/`.
> Prepared after reading every screen, the theme, the data layer, and `build.gradle.kts`.

---

## 0. TL;DR

The app is fully functional but visually rough because it leans on a **dated 2013 "Flat UI" color palette**, **default Material 3 components with no custom type/shape system**, **emoji used as icons**, **mixed Chinese/English chrome**, and **no bottom navigation** — so it reads like a stack of pushed forms rather than a product.

The redesign keeps all current logic and replaces only the **presentation layer**:

1. Build a real **design system** (tokens for color, type, shape, spacing) on **Material 3 Expressive** with optional dynamic color.
2. Add a **bottom navigation bar** (Home · Review · Library · Settings) so the app feels cohesive.
3. Rework each screen: a **hero "Due Today" card** on Home, a **flashcard surface with progress bar + filled color-coded rating buttons** on Review, **rich list rows** in the Library, etc.
4. Extract a shared **component library** (badges, section blocks, state dots, stat tiles, status banner).
5. Fix several small bugs found while reviewing (edge-to-edge status-bar contrast, deprecated `Divider`, dead imports, brittle status-color parsing, no audio button despite an `audioUrl` field).

Everything below is organized so it can be implemented in low-risk phases without touching the FSRS / sync logic.

---

## 1. Current state — what exists

Files reviewed:

| File | Role |
|------|------|
| `ui/theme/Theme.kt` | Custom palette + `WordFlowTheme` (light/dark `ColorScheme`) |
| `ui/Navigation.kt` | `NavHost` with routes: login → home → review / wordlist → worddetail |
| `ui/screen/LoginScreen.kt` | Email-code or pairing-code login (`TabRow`) |
| `ui/screen/HomeScreen.kt` | Dashboard: user card, stats card, daily-limit chips, action buttons |
| `ui/screen/ReviewScreen.kt` | Flashcard front → tap to reveal back → 4 rating buttons |
| `ui/screen/WordListScreen.kt` | Search + flat list with state label |
| `ui/screen/WordDetailScreen.kt` | Full word content view |
| `data/Models.kt`, `Store.kt`, `FsrsEngine.kt`, `SyncClient.kt` | Data + logic (out of scope for visual redesign) |

Stack: Jetpack Compose, `compose-bom:2024.02.00`, `material3`, `material-icons-extended`, navigation-compose 2.7.6, minSdk 26 / target 34. `enableEdgeToEdge()` is on.

---

## 2. Problems found (evidence-backed)

### 2.1 Design system

- **Dated palette.** `Theme.kt` uses the 2013 "Flat UI Colors" set (`Turquoise #1ABC9C`, `PeterRiver #3498DB`, `Alizarin #E74C3C`, `Carrot`, `SunFlower`…). It looks like a tutorial, not a brand. Light `primary = DarkSlate #2C3E50` is essentially dark gray — not distinctive.
- **No typography system.** Only default M3 type is used, so headings/body all look stock. No custom font, no scale emphasis.
- **No shape system.** Default M3 corners only; all cards look identical.
- **No spacing tokens.** Every screen hard-codes `8/12/16/24/32.dp` ad hoc, so rhythm is inconsistent.
- **Edge-to-edge is half-wired.** `Theme.kt` imports `Activity`, `SideEffect`, `LocalView`, `WindowCompat`, `toArgb` but **never uses them** — so the status bar gets no appearance handling and text may sit on low-contrast system bars.
- **Dead code.** The imports above are unused.
- **Deprecated API.** `Divider(...)` is used in Review/WordList/WordDetail; M3 prefers `HorizontalDivider`.

### 2.2 Cross-cutting / structure

- **No bottom navigation.** Every destination is a pushed screen from Home. Going Review → Library requires back-back-back. No `Scaffold` bottom-bar slot is used anywhere.
- **No shared components.** `BadgeText` is defined twice (ReviewScreen + WordListScreen) with different signatures; `Section`/`SectionText`/`StatColumn` are local. No `ui/components/` package.
- **Mixed language chrome.** Chinese and English are interleaved with no rule: `查词温故 WordFlow`, `释义` / `Definition` / `详细释义`, `记忆技巧`, `Word List`, `Start Review`, `Due for review`. Feels unfinished.
- **Emoji as icons everywhere:** `📧 📱 ✓ ↩ 🧠 📌 🚫 📚 🔄 📝 💡 📅`. Inconsistent and unprofessional vs. Material icons.
- **Status shown by string parsing.** `LoginScreen` colors the status line by `startsWith("Failed")` / `startsWith("Please")` — brittle. Should be a typed status.
- **Always starts at login.** `Navigation.kt` hard-codes `startDestination = "login"`, so a returning logged-in user sees the login screen every launch.
- **No real Settings screen.** `ReviewScreen`'s "Settings" action just `popBackStack()` (does nothing); Home has logout only.
- **Unused data.** `ParsedResult.audioUrl` / `pronunciation` exist but there is **no audio playback button** anywhere — a missed opportunity for a vocabulary app.

### 2.3 Per-screen

**LoginScreen**
- Plain stacked text title, no logo mark.
- `TabRow` uses emoji icons (`📧 📱`).
- Fields have no inline error state (errors only in a status line).
- "Code Sent ✓" uses an emoji.
- Lots of manual `Spacer`s; no vertical rhythm system.

**HomeScreen** — the weakest screen
- The **due count (the most important number)** is a plain `Text` line: `"Due for review: ${vm.dueCount}"`. Zero emphasis.
- Three identical flat Cards (user / stats / daily-limit) — no visual hierarchy.
- "Test" and "Pull All" buttons live *inside* the user card, mixing identity with actions.
- Daily-limit chips (`∞ 5 10 20 50`) are cramped in one row.
- "Start Review" is disabled when `dueCount == 0` with no explanation.
- Logout is a bare top-bar icon with no confirmation.

**ReviewScreen** — the core experience
- **No flashcard affordance:** the front is just centered text on the background — no card surface, elevation, or rounded container. Doesn't "feel" like a card.
- **No progress bar** in the top bar (the standard pattern for review apps); counts are only text.
- **Reveal has no animation** (`AnimatedVisibility` is imported but unused).
- Back side mixes emoji section labels (`🧠 📌 🚫 📚 🔄`) with non-emoji ones (`释义`, `Definition`); `Definition` is English, the rest Chinese.
- LLM definitions render as bare `Text` with emoji prefixes (`📝 `, `💡 `); examples have no container/italics.
- **Rating buttons are weak:** 4 outlined buttons with colored text in a cramped row. Best practice (Anki/AnkiDroid) is **filled, color-coded buttons** (Again red, Hard orange, Good green, Easy blue) — far easier to hit and read.
- Undo is a `TextButton("↩ Undo")` emoji; rating bar sits in a plain `Surface` rather than a proper bottom slot, risking IME/system-bar overlap under edge-to-edge.
- No audio button.
- Session-complete screen is a plain column; no celebratory feel.

**WordListScreen**
- Stat badges use odd abbreviations (`LRN`, `REV`) inconsistent with full labels elsewhere.
- List rows are the most minimal possible: word left, label right, divider. **No phonetic, no translation preview, no colored state dot, no avatar.** Hard to scan.
- No grouping (by state or alphabet), no pull-to-refresh.

**WordDetailScreen**
- Same emoji/mixed-language section issue as Review.
- No audio button, no actions (reset / mark known / jump to next due).

---

## 3. Design goals

1. **Cohesive product feel** — one design language, bottom nav, shared components.
2. **Focus on the review loop** — Home surfaces "due today" prominently; Review is tactile and satisfying.
3. **Modern, brand-appropriate look** — Material 3 Expressive, tonal color, real type scale, motion.
4. **Consistent language** — Chinese chrome (the audience), English for learning content; structured for i18n.
5. **Polish details** — status bars, animations, audio, accessibility, no emoji-as-icon.
6. **Zero logic risk** — touch only the presentation layer; keep `Store`/`FsrsEngine`/`SyncClient` as-is.

---

## 4. The redesign

### 4.1 Design system (`ui/theme/` + new `ui/components/`)

**Color — move to a tonal M3 system**
- Replace the Flat-UI palette with a **generated tonal palette** from one brand seed. Keep the "learning/growth" feel of the current turquoise but as proper M3 tonal roles (primary, primaryContainer, etc.).
- Define **semantic rating colors** as a small dedicated scale used only by rating buttons/stats:
  - Again → red (error tone), Hard → orange, Good → green (primary tone), Easy → blue.
- Offer **dynamic color (Material You)** on Android 12+ (`dynamicColorScheme`), with the brand scheme as the fallback / opt-out.
- Keep light + dark both polished; verify dark-mode contrast (the current dark `0xFF1A1A2E` navy is fine but needs the whole tonal set rechecked).

**Typography**
- Define a `Typography` with a clear scale (display/headline/title/body/label) and **weight ramp**. Consider a variable font (e.g. a humanist sans) for the brand while keeping system default as fallback.
- Word headings use `displayMedium`/`headlineLarge` with weight; section labels use `labelLarge` uppercased with letter-spacing; examples use body italic.

**Shape**
- Custom `Shapes`: small 8dp (chips/badges), medium 16dp (cards), large 24dp (hero card / sheet). Replaces identical default corners.

**Spacing tokens**
- New `Dimens` object: `xs=4, sm=8, md=12, lg=16, xl=24, xxl=32`. Screens reference tokens, not raw dp.

**Motion**
- Adopt M3 Expressive motion specs: card flip/reveal (`AnimatedContent` crossfade + scale), button press scale, progress-bar fill, session-complete celebration.
- Reveal the answer with an `AnimatedContent` transition (front ↔ back), not an instant swap.

**System bars (fix the edge-to-edge gap)**
- In `WordFlowTheme`, use a `SideEffect` + `WindowCompat` to set status-bar icon appearance (light/dark icons) from the current color scheme, so content never sits on low-contrast bars.

**Shared component library (`ui/components/`)**
Extract and reuse:
- `StatusBanner` (typed: info/success/error/loading) — replaces string-prefix status logic.
- `WordChip` / `StateBadge` / `StateDot` (colored dot + label, single source of truth).
- `StatTile` (number + label + optional color) for Home and session summary.
- `SectionBlock` (label + body container with consistent spacing) — replaces ad-hoc `Section`/`SectionText`.
- `ExampleBlock` (container for an English example + Chinese gloss, italics).
- `RatingButtonRow` (the 4 filled color buttons + interval preview).
- `FlashCard` (elevated surface container used by Review).
- `AudioButton` (plays `audioUrl` / TTS fallback).
- `EmptyState` (icon + title + action).

### 4.2 App structure & navigation

- Wrap the whole app in one root `Scaffold` that hosts the **bottom `NavigationBar`** and lets each tab screen supply its own `TopAppBar`. Use `popUpTo` + `saveState` + `restoreState` so tab switches keep state.
- **Bottom nav:** Home 🏠 · Review 📖 · Library 📚 · Settings ⚙ (Material icons, not emoji). Hide the bar on Login and inside an active Review session for immersion.
- **Smart start destination:** if `store.isLoggedIn`, start at `home`; else `login`.
- Add the missing **Settings screen** (server address, daily limit, sync now, last sync, logout, about). Move logout + sync controls out of Home into Settings.

### 4.3 Screen-by-screen redesign

#### Login
```
┌─────────────────────────────┐
│         [logo mark]         │
│          查词温故             │
│          WordFlow            │
│   记住每一个单词               │
│                              │
│  ┌─────────┬──────────────┐  │
│  │  邮箱    │  配对码       │  │
│  └─────────┴──────────────┘  │
│  ┌────────────────────────┐  │
│  │ ✉  邮箱                 │  │  ← inline error state on field
│  └────────────────────────┘  │
│  ┌────────────────────────┐  │
│  │      发送验证码          │  │
│  └────────────────────────┘  │
│  需要帮助？查看配对码说明       │
└─────────────────────────────┘
```
- Logo mark (vector) + Chinese title + English subtitle + one-line value prop.
- `TabRow` with **Material icons** (`Email`, `Link`), no emoji.
- Inline field errors (outlined field `isError` + supporting text) instead of a status line; keep a `StatusBanner` only for async success/failure.
- Buttons use brand primary fill, larger touch targets.

#### Home — dashboard with a hero due card
```
┌─────────────────────────────┐
│ WordFlow              ⚙     │
├─────────────────────────────┤
│ ┌─────────────────────────┐ │
│ │  今日待复习   DUE TODAY  │ │  ← hero card, primary container
│ │      42                  │ │
│ │  个单词                   │ │
│ │  ┌───────────────────┐   │ │
│ │  │   开始复习      →  │   │ │  ← primary CTA inline
│ │  └───────────────────┘   │ │
│ └─────────────────────────┘ │
│  连续 7 天 · 2 分钟前已同步   │  ← secondary line (streak + sync)
│ ┌───────────┬─────────────┐ │
│ │  1,240    │   12 / 20   │ │  ← StatTile ×2
│ │  单词总数  │  今日新词    │ │
│ └───────────┴─────────────┘ │
│ 今日新词上限   ∞  5  10  20  │  ← chips with selected highlight
├─────────────────────────────┤
│ 🏠首页  📖复习  📚词库  ⚙设置 │
└─────────────────────────────┘
```
- **Hero card** makes the due number the visual centerpiece (`displayLarge`) with an inline "开始复习" CTA. Disabled state shows "暂无待复习" with a gentle nudge instead of a dead button.
- Sync/account controls **move to Settings**; Home shows only a compact "synced X ago" status.
- `StatTile` grid replaces the three identical cards.

#### Review — tactile flashcards + filled rating buttons
```
┌─────────────────────────────┐
│ ←  7 / 42            ↩  ⋮   │  ← count + undo + menu
│ ━━━━━━━━━░░░░░░░░░░░░░░░░░░ │  ← LinearProgressIndicator in top bar
├─────────────────────────────┤
│         ┌───────────────┐    │
│         │               │    │
│         │ perseverance  │    │  ← elevated FlashCard surface,
│         │ /ˌpɜːsɪˈvɪə/ 🔊│    │     rounded 24dp, tap to reveal
│         │               │    │
│         │  轻点查看答案 → │    │
│         └───────────────┘    │
├─────────────────────────────┤
│ (after reveal — AnimatedContent crossfade)
│ ┌──────┬──────┬──────┬─────┐ │
│ │ Again│ Hard │ Good │ Easy│ │  ← FILLED, color-coded
│ │ <1d  │  4d  │  9d  │ 15d │ │
│ └──────┴──────┴──────┴─────┘ │
└─────────────────────────────┘
```
- Card is a real **`Card(elevated)` surface** with rounded corners and subtle shadow — feels like a physical card.
- **Progress bar** in the top app bar replaces text-only counts.
- **Reveal = `AnimatedContent`** (crossfade + slight scale), not an instant swap.
- **Filled color-coded rating buttons** (Again red / Hard orange / Good green / Easy blue), each showing the interval preview; large touch targets; keyboard `1/2/3/4` + Space-to-reveal for external keyboards.
- Undo = icon button in the top bar (not an emoji text button).
- Audio button (🔊) plays `audioUrl` (TTS fallback).
- Back side uses `SectionBlock` + `ExampleBlock`; **all-Chinese section labels** (`释义`, `详细释义`, `记忆技巧`, `近义词`, `反义词`, `词源`, `词形变化`); examples in italic containers — no emoji prefixes.
- Session-complete: keep stats but add a small celebratory motion + a clear "继续复习 / 返回首页" choice; remaining-due count surfaced.

#### Library (was WordList)
```
┌─────────────────────────────┐
│ ←  词库                 🔍   │
│ ┌─────────────────────────┐ │
│ │ 🔍 搜索单词…              │ │
│ └─────────────────────────┘ │
│ ● 新词 42  ● 学习中 8  ● 复习 5 │  ← StateDot legend (full labels)
├─────────────────────────────┤
│ ●  perseverance               │
│    /ˌpɜːsɪ/  ·  n. 毅力        │  ← phonetic + translation preview
│ ───────────────────────────  │
│ ●  eloquent                   │
│    /ˈeləkwənt/ · adj. 雄辩的  │
└─────────────────────────────┘
```
- Rich rows: **colored state dot + word + phonetic + translation preview** (one line) — scannable at a glance.
- Full state labels (新词 / 学习中 / 复习 / 重学), not abbreviations.
- Optional: group headers by state, alphabetical index, pull-to-refresh to sync.

#### WordDetail
```
┌─────────────────────────────┐
│ ←  perseverance       🔊 ⋮  │
├─────────────────────────────┤
│ perseverance                  │
│ /ˌpɜːsɪˈvɪərəns/ 🔊   ● 复习  │
│ 4 天后到期 · 间隔 9 天          │
│ ───────────────────────────  │
│ 释义                          │
│ n. 毅力；不屈不挠              │
│ 详细释义                       │
│ ┌──────────────────────────┐ │
│ │ n. persistence in a task │ │  ← ExampleBlock container
│ │ ex: "Her perseverance…"  │ │
│ │     "她的毅力……"          │ │
│ └──────────────────────────┘ │
│ 记忆技巧 / 近义词 / 反义词 / 词源│
│ ───────────────────────────  │
│ [ 重置进度 ]   [ 标记已掌握 ]  │  ← actions row
└─────────────────────────────┘
```
- Consistent `SectionBlock`/`ExampleBlock`, all-Chinese labels, no emoji.
- Audio button.
- Actions: reset review state / mark as known (calls existing `Store` methods).

#### Settings (new)
```
┌─────────────────────────────┐
│ ←  设置                       │
├─────────────────────────────┤
│ 账户                          │
│   user@example.com           │
│   [ 退出登录 ]                │
│ 同步                          │
│   服务器地址  https://…       │
│   上次同步    2 分钟前         │
│   [ 立即同步 ]  [ 测试连接 ]  │
│ 学习                          │
│   每日新词上限   ∞ 5 10 20 50 │
│ 外观                          │
│   主题  跟随系统 / 浅色 / 深色 │
│   动态取色 (Material You)  开 │
│ 关于                          │
│   WordFlow 1.0.0             │
└─────────────────────────────┘
```
- Consolidates everything currently scattered in Home (sync, daily limit, logout) plus theme controls and about.
- Theme toggle enables the new dynamic-color option.

### 4.4 Language & i18n
- **Rule:** chrome (buttons, labels, nav) in **Chinese** for the target audience; English-learning **content** (definitions, examples) stays English.
- Move all user-facing strings to `res/values/strings.xml` (and `values-en` later). Hard-coded strings become `stringResource(...)`.
- Replace every emoji-as-icon with a Material icon from `material-icons-extended` (already a dependency).

### 4.5 Accessibility
- Min 48dp touch targets; rating buttons tested at small widths.
- Color contrast AA for both themes (rating colors especially).
- `contentDescription` on all icon buttons (some are missing today, e.g. several `null` descriptions).
- Support font scale / large text without breaking layouts (avoid fixed heights).
- Reduce-motion: skip celebration animations when `LocalAccessibilityManager` says so.

### 4.6 Bugs to fix while redesigning (found during review)
| Bug | Where | Fix |
|-----|-------|-----|
| Edge-to-edge status bar has no appearance handling | `Theme.kt` | `SideEffect` + `WindowCompat.setDecorFitsSystemWindows` / icon appearance from color scheme |
| Dead imports (`Activity`, `SideEffect`, `LocalView`, `WindowCompat`, `toArgb`) | `Theme.kt` | Remove or actually use |
| Deprecated `Divider` | Review/WordList/WordDetail | `HorizontalDivider` |
| Status color decided by string prefix | `LoginScreen.kt` | Typed `StatusBanner` |
| Always starts at login | `Navigation.kt` | Choose start destination by `store.isLoggedIn` |
| Review "Settings" does nothing | `ReviewScreen.kt` | Wire to real Settings screen |
| `audioUrl`/`pronunciation` unused | all screens | Add `AudioButton` |
| Duplicate `BadgeText` | Review + WordList | One shared `StateBadge` |
| `AnimatedVisibility` imported, unused | `ReviewScreen.kt` | Use for reveal transition |
| `NetworkSecurityConfig` + `usesCleartextTraffic=false` with a custom port server | `AndroidManifest.xml` | Verify config covers the prod domain (out of visual scope, but note it) |

_Bugs below were confirmed via real-device screenshots (logged-in user, real sync data):_
| Empty email on Home after pairing-code login | `LoginScreen.kt` (`verifyPairCode`) | Pairing login sets the token but never saves `userEmail` → Home shows `"Email: "` blank. Set an identifier (or reuse email field) on pairing login, same as the email path |
| Double-slash phonetic (`//həˈloʊ//`) | `ReviewScreen.kt`, `WordDetailScreen.kt` (and any `"/$it/"` wrap) | Server `phonetic` already includes slashes, and the UI wraps again. Strip leading/trailing `/` before wrapping, or only wrap when absent |
| Raw exam tags shown verbatim (`zk gk`) | Review/WordDetail badges | ECDICT tag codes are displayed as-is. Map to friendly labels (`zk`→中考, `gk`→高考, `cet4`/`cet6`→CET-4/6, `gre`, `toefl`, `ielts`, `ky`→考研, …) |

---

## 5. Implementation phases (low-risk, incremental)

Each phase ships independently and never touches FSRS/sync logic.

**Phase A — Design system foundation** (~foundation for everything)
- New `Theme.kt` (tonal palette, dynamic color, `Typography`, `Shapes`, status-bar handling).
- New `Dimens` spacing tokens.
- New `ui/components/` package (`StatusBanner`, `StateBadge`/`StateDot`, `StatTile`, `SectionBlock`, `ExampleBlock`, `AudioButton`, `EmptyState`).
- Strings extracted to `strings.xml`.
- Unit-check: app still compiles and existing screens look "the same but cleaner".

**Phase B — Navigation & Settings**
- Root `Scaffold` + bottom `NavigationBar` (Home/Review/Library/Settings) with saved state.
- Smart start destination.
- New `SettingsScreen`; move sync/logout/daily-limit out of Home.

**Phase C — Home redesign**
- Hero "今日待复习" card + `StatTile` grid + secondary status line.

**Phase D — Review redesign** (highest user value)
- `FlashCard` surface, top-bar progress, `AnimatedContent` reveal, filled color-coded `RatingButtonRow`, icon undo, audio button, all-Chinese `SectionBlock`/`ExampleBlock`, polished session-complete.

**Phase E — Library + WordDetail redesign**
- Rich list rows, full state labels, optional grouping/pull-to-refresh.
- WordDetail: consistent blocks, audio, actions row.

**Phase F — Polish**
- Motion tuning, dark-mode contrast pass, accessibility audit, reduce-motion, dynamic-color verification on Android 12+.

---

## 6. Out of scope (for this redesign)
- Any change to `FsrsEngine`, `Store`, `SyncClient`, or the SyncServer API.
- New features beyond UI (e.g. deck/folder management, statistics charts) — note as future work only.
- Icon launcher assets / app icon redesign (can be a follow-up).
- Real-device TLS/network config (already tracked in `task_plan.md`).

---

## 7. Open questions for you
1. **Primary UI language:** confirm Chinese chrome + English learning content (recommended), or full English, or a toggle?
2. **Dynamic color (Material You):** enable by default on Android 12+, or keep a fixed brand palette?
3. **Brand seed color:** keep a teal/green in the spirit of the current turquoise, or pick a new brand color?
4. **Bottom nav vs. keep Home as hub:** happy to add the 4-tab bottom nav, or do you prefer the current single-hub model?
5. **Audio:** is `audioUrl` reliably populated by the server, or should we lean on Android TTS as the primary pronunciation source?
6. **Theme switcher:** do you want a manual light/dark/system toggle in Settings, or follow system only?

---

_Once you confirm the direction (and answer the open questions), I can start implementing Phase A._
