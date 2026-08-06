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
