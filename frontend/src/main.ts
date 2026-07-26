import { Events } from "@wailsio/runtime";
import { LookupWordFast, LookupWordLLMFast, LookupWordCached, CacheResult, GetConfig, SaveConfig, GetPromptConfig, SavePromptConfig, TestPrompt, GetCacheStats, GetPromptDebugInfo, SetAutoStart, GetAutoStart } from "../bindings/wordflow/dictservice.js";
import { EcdictIsAvailable, ImportEcdict } from "../bindings/wordflow/ecdictservice.js";
import { GetHistory, AddHistory, DeleteHistory, ClearHistory, GetHistoryEntry } from "../bindings/wordflow/historyservice.js";
import { GetSyncConfig, SaveSyncConfig, TestConnection, PushToServer, PullFromServer, RequestQrCode, PollQrCodeStatus } from "../bindings/wordflow/syncservice.js";

// ============================================================
// PromptConfig - LLM prompt customization (mirrors Go PromptConfig)
// ============================================================
interface PromptField {
    key: string;
    label: string;
    icon: string;
    type: "string" | "text" | "list" | "definitions";
    desc: string;
    enabled: boolean;
    builtin: boolean;
}

interface PromptConfig {
    systemPrompt: string;
    fields: PromptField[];
    extraRequirements: string;
    temperature: number;
    maxTokens: number;
}

const DEFINITIONS_SCHEMA_BLOCK = `  "definitions": [
    {
      "pos": "词性（如 n. / v. / adj. 等）",
      "meaning": "中文释义",
      "english_example": "英文例句",
      "chinese_example": "例句中文翻译"
    }
  ]`;

const SPECIAL_FIELD_KEYS = ["word", "phonetic", "pronunciation", "definitions"];

function defaultPromptConfig(): PromptConfig {
    return {
        systemPrompt: "你是一个专业的英语词典助手，总是以纯JSON格式回复，不包含markdown标记。用户会给你一个英语单词或短语，你必须解释它。",
        extraRequirements: "",
        temperature: 0.3,
        maxTokens: 2000,
        fields: [
            { key: "word", label: "单词", icon: "🔤", type: "string", desc: "被查询的英语单词或短语", enabled: true, builtin: true },
            { key: "phonetic", label: "音标", icon: "🎵", type: "string", desc: "音标（国际音标）", enabled: true, builtin: true },
            { key: "pronunciation", label: "发音提示", icon: "🗣️", type: "string", desc: "发音提示（用中文近似标注）", enabled: true, builtin: true },
            { key: "definitions", label: "详细释义", icon: "📖", type: "definitions", desc: "包含词性、释义、英文例句及中文翻译", enabled: true, builtin: true },
            { key: "memory_tips", label: "记忆技巧", icon: "🧠", type: "text", desc: "帮助记忆的技巧、词根词缀分析、联想记忆等", enabled: true, builtin: true },
            { key: "synonyms", label: "近义词", icon: "📌", type: "list", desc: "近义词（如有）", enabled: true, builtin: true },
            { key: "antonyms", label: "反义词", icon: "🚫", type: "list", desc: "反义词（如有）", enabled: true, builtin: true },
            { key: "etymology", label: "词源", icon: "📚", type: "text", desc: "词源小故事（简短有趣）", enabled: true, builtin: true },
        ],
    };
}

let promptConfig: PromptConfig | null = null;

/** Report whether a field is enabled. Unknown/missing config defaults to shown (backward compat). */
function fieldEnabled(cfg: PromptConfig | null, key: string): boolean {
    if (!cfg) return true;
    for (const f of cfg.fields) if (f.key === key) return f.enabled;
    return true;
}

/** Build a preview of the assembled user prompt (mirrors Go buildUserPrompt, without ECDICT context). */
function buildPromptPreview(cfg: PromptConfig, word: string): string {
    const lines: string[] = [];
    for (const f of cfg.fields) {
        if (!f.enabled) continue;
        if (f.key === "word") lines.push(`  "word": "..."`); // placeholder — actual word in variable suffix
        else if (f.key === "definitions") lines.push(DEFINITIONS_SCHEMA_BLOCK);
        else lines.push(`  "${f.key}": "${f.desc}"`);
    }
    const schema = "{\n" + lines.join(",\n") + "\n}";
    let closing = "\n请确保返回纯JSON，不要有其他内容。";
    if (fieldEnabled(cfg, "definitions")) closing += "如果有多个词性，请在definitions中分别列出。";
    let extra = "";
    if (cfg.extraRequirements.trim()) extra = "\n\n额外要求：" + cfg.extraRequirements;
    const staticPrefix = `请对英语单词或短语进行详细解释，严格按照以下JSON格式返回（不要包含markdown代码块标记）：\n\n${schema}${closing}${extra}`;
    const variableSuffix = `\n\n---\n查询单词：${word}\n\n（实际查询时还会附带 ECDICT 已知信息以避免重复）`;
    return `[STATIC PREFIX — cacheable by LLM provider]\n${staticPrefix}\n\n[VARIABLE SUFFIX — changes per word]\n${variableSuffix}`;
}

// Load prompt configuration at startup
GetPromptConfig().then(json => {
    if (json) {
        try { promptConfig = JSON.parse(json); } catch { /* keep null */ }
    }
    if (!promptConfig) promptConfig = defaultPromptConfig();
}).catch(err => {
    promptConfig = defaultPromptConfig();
    console.error("[WordFlow] Failed to load prompt config:", err);
});

// ============================================================
// DOM Elements
// ============================================================
const searchInput = document.getElementById("search-input") as HTMLInputElement;
const btnClearSearch = document.getElementById("btn-clear-search") as HTMLButtonElement;
const btnSearch = document.getElementById("btn-search") as HTMLButtonElement;
const btnHistory = document.getElementById("btn-history") as HTMLButtonElement;
const btnSettings = document.getElementById("btn-settings") as HTMLButtonElement;
const btnCloseHistory = document.getElementById("btn-close-history") as HTMLButtonElement;
const btnCloseSettings = document.getElementById("btn-close-settings") as HTMLButtonElement;
const btnClearHistory = document.getElementById("btn-clear-history") as HTMLButtonElement;
const btnSaveConfig = document.getElementById("btn-save-config") as HTMLButtonElement;
const btnCloseModal = document.getElementById("btn-close-modal") as HTMLButtonElement;
const btnImportEcdict = document.getElementById("btn-import-ecdict") as HTMLButtonElement;

const loadingEl = document.getElementById("loading") as HTMLDivElement;
const errorEl = document.getElementById("error") as HTMLDivElement;
const errorText = document.getElementById("error-text") as HTMLSpanElement;
const resultEl = document.getElementById("result") as HTMLDivElement;
const historyPanel = document.getElementById("history-panel") as HTMLDivElement;
const settingsPanel = document.getElementById("settings-panel") as HTMLDivElement;
const historyList = document.getElementById("history-list") as HTMLDivElement;
const historyDetailModal = document.getElementById("history-detail-modal") as HTMLDivElement;
const modalTitle = document.getElementById("modal-title") as HTMLHeadingElement;
const modalBody = document.getElementById("modal-body") as HTMLDivElement;

const inputApiKey = document.getElementById("api-key") as HTMLInputElement;
const inputApiUrl = document.getElementById("api-url") as HTMLInputElement;
const inputModelName = document.getElementById("model-name") as HTMLInputElement;
const inputShortcutKey = document.getElementById("shortcut-key") as HTMLInputElement;
const btnRecordShortcut = document.getElementById("btn-record-shortcut") as HTMLButtonElement;
const btnToggleKey = document.getElementById("btn-toggle-key") as HTMLButtonElement;
const autoStartToggle = document.getElementById("auto-start-toggle") as HTMLInputElement;

// ============================================================
// State
// ============================================================
let isSearching = false;
let currentWord = "";
let ecdictAvailable = false;

// Check ECDICT availability on load
EcdictIsAvailable().then(available => {
    ecdictAvailable = available;
    if (available) {
        console.log("[WordFlow] ECDICT offline dictionary is available");
    } else {
        console.log("[WordFlow] ECDICT offline dictionary not found - run import first");
    }
}).catch(console.error);

// ============================================================
// Exam tag display
// ============================================================
const EXAM_TAG_MAP: Record<string, string> = {
    gk: "高考",
    cet4: "四级",
    cet6: "六级",
    ky: "考研",
    toefl: "托福",
    ielts: "雅思",
    gre: "GRE",
    sat: "SAT",
};

function formatExamTags(tag: string | null | undefined): { short: string; full: string }[] {
    if (!tag) return [];
    return tag
        .split(/\s+/)
        .filter(Boolean)
        .map(t => ({ short: EXAM_TAG_MAP[t] || t.toUpperCase(), full: t }));
}

// ============================================================
// Exchange (inflected forms) parser
// ============================================================
const EXCHANGE_LABELS: Record<string, string> = {
    d: "过去式",
    p: "过去分词",
    i: "现在分词",
    "3": "第三人称单数",
    s: "名词复数",
    r: "比较级",
    t: "最高级",
};

function parseExchange(exchange: string | null | undefined): { label: string; form: string }[] {
    if (!exchange) return [];
    const forms: { label: string; form: string }[] = [];
    for (const part of exchange.split("/")) {
        const [type, form] = part.split(":");
        if (form && type && EXCHANGE_LABELS[type]) {
            forms.push({ label: EXCHANGE_LABELS[type], form });
        }
    }
    return forms;
}

// ============================================================
// Search - Two-phase lookup: ECDICT fast → LLM enrichment
// ============================================================
async function doSearch(word: string) {
    word = word.trim();
    if (!word) {
        showToast("请输入要查询的单词");
        return;
    }
    if (isSearching) {
        // If searching a different word, mark current as stale so the in-flight search aborts
        if (currentWord !== word) {
            currentWord = word; // This will cause the in-flight search to detect stale and abort
        }
        return;
    }

    isSearching = true;
    currentWord = word;
    searchInput.value = word;
    hideError();
    resultEl.classList.add("hidden");
    loadingEl.classList.remove("hidden");

    // Track merged data for history saving
    let mergedData: any = { word };
    let ecdictData: any = null;
    let llmData: any = null;

    try {
        // ── Phase 0: Check cache (instant) ──
        const cacheStartTime = performance.now();
        const cachedResult = await LookupWordCached(word);
        const cacheDuration = performance.now() - cacheStartTime;
        if (currentWord !== word) { isSearching = false; return; } // Stale query

        if (cachedResult) {
            console.log(`[LLM-DEBUG] Cache HIT in ${cacheDuration.toFixed(0)}ms, skipping ECDICT+LLM`);
            try {
                mergedData = JSON.parse(cachedResult);
            } catch {
                mergedData = { word, _rawCache: cachedResult };
            }
            loadingEl.classList.add("hidden");
            resultEl.innerHTML = renderWordResult(mergedData, false);
            resultEl.classList.remove("hidden");
            resultEl.scrollTop = 0;
            isSearching = false;
            return;
        }

        console.log(`[LLM-DEBUG] Cache MISS in ${cacheDuration.toFixed(0)}ms, proceeding to ECDICT+LLM`);
        // ── Phase 1: ECDICT fast lookup (~10ms) ──
        const ecdictStartTime = performance.now();
        console.log(`[LLM-DEBUG] === Phase 1: Starting ECDICT lookup for "${word}" ===`);
        const ecdictResult = await LookupWordFast(word);
        const ecdictDuration = performance.now() - ecdictStartTime;
        console.log(`[LLM-DEBUG] ECDICT lookup completed in ${ecdictDuration.toFixed(0)}ms, result: ${ecdictResult ? 'found' : 'not found'}`);
        if (currentWord !== word) { isSearching = false; return; } // Stale query

        if (ecdictResult) {
            try {
                ecdictData = JSON.parse(ecdictResult);
                mergedData = { ...mergedData, ...ecdictData };

                // Show ECDICT result immediately
                loadingEl.classList.add("hidden");
                resultEl.innerHTML = renderWordResult(mergedData, true); // true = show "loading more" indicator
                resultEl.classList.remove("hidden");
                resultEl.scrollTop = 0;
            } catch (e) {
                console.error("[WordFlow] Failed to parse ECDICT result:", e);
            }
        }

        // ── Phase 2: LLM enrichment (slow, ~2-10s) ──
        const llmStartTime = performance.now();
        console.log(`[LLM-DEBUG] === Phase 2: Starting LLM lookup for "${word}" ===`);
        try {
            const llmResult = await LookupWordLLMFast(word);
            const llmDuration = performance.now() - llmStartTime;
            console.log(`[LLM-DEBUG] LLM call completed in ${llmDuration.toFixed(0)}ms`);
            console.log(`[LLM-DEBUG] LLM result type: ${typeof llmResult}, length: ${llmResult?.length ?? 0}`);
            if (llmResult) {
                console.log(`[LLM-DEBUG] LLM result preview: ${llmResult.substring(0, 200)}`);
            } else {
                console.log(`[LLM-DEBUG] LLM returned empty/null result`);
            }
            if (currentWord !== word) { isSearching = false; return; } // Stale query

            if (llmResult) {
                let parsed: any = null;
                let rawResult = llmResult.trim();

                // Robust JSON parsing
                try {
                    parsed = JSON.parse(rawResult);
                    console.log(`[LLM-DEBUG] JSON parsed successfully on first try`);
                } catch (parseErr) {
                    console.warn(`[LLM-DEBUG] JSON parse failed on first try: ${parseErr}, attempting recovery...`);
                    const jsonMatch = rawResult.match(/\{[\s\S]*\}/);
                    if (jsonMatch) {
                        try {
                            parsed = JSON.parse(jsonMatch[0]);
                            console.log(`[LLM-DEBUG] JSON parsed successfully after regex extraction`);
                        } catch {
                            let cleaned = jsonMatch[0].replace(/[\x00-\x1f\x7f]/g, (ch) => {
                                if (ch === '\n' || ch === '\r' || ch === '\t') return ch;
                                return '';
                            });
                            try {
                                parsed = JSON.parse(cleaned);
                                console.log(`[LLM-DEBUG] JSON parsed successfully after control char cleaning`);
                            } catch (finalErr) {
                                console.error(`[LLM-DEBUG] JSON parse failed completely: ${finalErr}`);
                                console.error(`[LLM-DEBUG] Raw result that failed to parse (first 500 chars): ${rawResult.substring(0, 500)}`);
                                // Give up on LLM parse, keep ECDICT result
                            }
                        }
                    }
                }

                if (parsed) {
                    llmData = parsed;
                    // Merge: ECDICT provides base, LLM enriches
                    // ECDICT fields (phonetic, translation, tag, collins, etc.) take priority
                    // LLM adds: definitions with examples, memory_tips, synonyms, antonyms, etymology
                    mergedData = mergeResults(ecdictData, llmData);
                }
            }
        } catch (err: any) {
            const llmDuration = performance.now() - llmStartTime;
            console.error(`[LLM-DEBUG] LLM call FAILED after ${llmDuration.toFixed(0)}ms:`, err);
            console.error(`[LLM-DEBUG] Error type: ${typeof err}, message: ${String(err)}`);
            // LLM failed - if we have ECDICT data, that's fine
            if (!ecdictData) {
                loadingEl.classList.add("hidden");
                showError(String(err));
                isSearching = false;
                return;
            }
            // Show ECDICT result with a subtle note
            mergedData._llmError = String(err);
        }

        // ── Final render ──
        loadingEl.classList.add("hidden");

        if (!ecdictData && !llmData) {
            // Neither source found the word
            resultEl.innerHTML = `<div class="result-card">
                <div class="word-header">
                    <span class="word-text">${escapeHtml(word)}</span>
                </div>
                <div class="section-content" style="color:var(--text-muted);margin-top:8px;">
                    未找到该单词的释义。请检查拼写是否正确。
                </div>
            </div>`;
            resultEl.classList.remove("hidden");
            isSearching = false;
            return;
        }

        resultEl.innerHTML = renderWordResult(mergedData, false);
        resultEl.classList.remove("hidden");
        resultEl.scrollTop = 0;

        // Save to history (merged result) and in-memory cache
        const mergedJson = JSON.stringify(mergedData);
        AddHistory(word, mergedJson).catch(console.error);
        CacheResult(word, mergedJson).catch(console.error);

    } catch (err: any) {
        loadingEl.classList.add("hidden");
        showError(String(err));
    } finally {
        isSearching = false;
    }
}

/**
 * Merge ECDICT + LLM results.
 * ECDICT provides: phonetic, translation, tag, collins, bnc, frq, exchange, pos
 * LLM provides: definitions with examples, memory_tips, synonyms, antonyms, etymology, pronunciation
 * Strategy: ECDICT base fields take priority; LLM fills in richer content
 */
function mergeResults(ecdict: any, llm: any): any {
    const cfg = promptConfig;
    const merged: any = { word: llm?.word || ecdict?.word || "" };

    // Phonetic: prefer ECDICT IPA when available; otherwise LLM
    if (fieldEnabled(cfg, "phonetic")) {
        if (ecdict?.phonetic && ecdict.phonetic.startsWith("/")) {
            merged.phonetic = ecdict.phonetic;
        } else if (llm?.phonetic) {
            merged.phonetic = llm.phonetic;
        } else {
            merged.phonetic = ecdict?.phonetic || llm?.phonetic || "";
        }
    }

    // Pronunciation: only from LLM
    if (fieldEnabled(cfg, "pronunciation")) {
        merged.pronunciation = llm?.pronunciation || "";
    }

    // Translation: ECDICT Chinese translation is authoritative (always shown)
    merged.translation = ecdict?.translation || llm?.translation || "";

    // Definitions: LLM structured definitions preferred, ECDICT fallback
    if (fieldEnabled(cfg, "definitions")) {
        if (llm?.definitions && Array.isArray(llm.definitions) && llm.definitions.length > 0) {
            merged.definitions = llm.definitions;
        } else if (ecdict?.definition) {
            merged.definitions = parseEcdictDefinitions(ecdict.definition, ecdict?.pos);
        } else {
            merged.definitions = [];
        }
    }

    // Other LLM fields driven by prompt config (skip special keys handled above)
    if (cfg) {
        for (const f of cfg.fields) {
            if (!f.enabled || SPECIAL_FIELD_KEYS.includes(f.key)) continue;
            const v = llm?.[f.key];
            merged[f.key] = v !== undefined && v !== null ? v : "";
        }
    } else {
        // Fallback: old hardcoded fields when config not yet loaded
        merged.memory_tips = llm?.memory_tips || "";
        merged.synonyms = llm?.synonyms || "";
        merged.antonyms = llm?.antonyms || "";
        merged.etymology = llm?.etymology || "";
    }

    // ECDICT-specific fields (always present when available)
    merged.collins = ecdict?.collins ?? null;
    merged.oxford = ecdict?.oxford ?? null;
    merged.tag = ecdict?.tag || null;
    merged.bnc = ecdict?.bnc ?? null;
    merged.frq = ecdict?.frq ?? null;
    merged.exchange = ecdict?.exchange || null;

    // Source tracking
    merged._sources = [];
    if (ecdict) merged._sources.push("ECDICT");
    if (llm) merged._sources.push("LLM");

    return merged;
}

/**
 * Parse ECDICT English definitions into structured format
 */
function parseEcdictDefinitions(definition: string, pos?: string): any[] {
    if (!definition) return [];
    const lines = definition.split("\n").filter(Boolean);
    if (lines.length === 0) return [];

    // Group by part of speech prefixes like "n.", "v.", "adj."
    const groups: any[] = [];
    let currentGroup: any = null;

    for (const line of lines) {
        const posMatch = line.match(/^([a-z]+\.)\s*/);
        if (posMatch) {
            currentGroup = {
                pos: posMatch[1],
                meaning: line.replace(posMatch[0], "").trim(),
            };
            groups.push(currentGroup);
        } else if (currentGroup) {
            currentGroup.meaning += "; " + line.trim();
        } else {
            // No POS prefix, use the provided pos or default
            currentGroup = {
                pos: pos || "",
                meaning: line.trim(),
            };
            groups.push(currentGroup);
        }
    }

    return groups;
}

// ============================================================
// Render
// ============================================================
function renderWordResult(data: any, isLoadingMore: boolean): string {
    const cfg = promptConfig;
    let html = '<div class="result-card">';

    // Word header
    html += '<div class="word-header">';
    html += `<span class="word-text">${escapeHtml(data.word || "")}</span>`;
    if (fieldEnabled(cfg, "phonetic") && data.phonetic) {
        html += `<span class="word-phonetic">${escapeHtml(data.phonetic)}</span>`;
    }
    if (fieldEnabled(cfg, "pronunciation") && data.pronunciation) {
        html += `<span class="word-pronunciation">${escapeHtml(data.pronunciation)}</span>`;
    }
    html += "</div>";

    // ── ECDICT badges: exam tags, Collins stars, frequency ──
    const examTags = formatExamTags(data.tag);
    const hasBadges = examTags.length > 0 || data.collins || data.oxford || (data.bnc && data.bnc < 10000);

    if (hasBadges) {
        html += '<div class="ecdict-badges">';
        for (const t of examTags) {
            html += `<span class="badge badge-exam" title="${escapeHtml(t.full)}">${escapeHtml(t.short)}</span>`;
        }
        if (data.collins) {
            html += `<span class="badge badge-collins">${"★".repeat(Math.min(data.collins, 5))}</span>`;
        }
        if (data.oxford) {
            html += `<span class="badge badge-oxford">Oxford 3000</span>`;
        }
        if (data.bnc && data.bnc < 10000) {
            html += `<span class="badge badge-freq">BNC #${data.bnc}</span>`;
        }
        html += '</div>';
    }

    // ── Chinese Translation (from ECDICT - fast, always shown) ──
    if (data.translation) {
        html += '<div class="translation-block">';
        const transLines = String(data.translation).split("\n").filter(Boolean);
        for (const line of transLines) {
            html += `<div class="translation-line">${escapeHtml(line)}</div>`;
        }
        html += '</div>';
    }

    // ── Definitions (from LLM with examples, or ECDICT fallback) ──
    if (fieldEnabled(cfg, "definitions") && data.definitions && Array.isArray(data.definitions)) {
        for (const def of data.definitions) {
            html += '<div class="def-item">';
            if (def.pos) html += `<span class="def-pos">${escapeHtml(def.pos)}</span>`;
            if (def.meaning) html += `<div class="def-meaning">${escapeHtml(def.meaning)}</div>`;
            if (def.english_example) {
                html += `<div class="def-example def-example-en">📝 ${escapeHtml(def.english_example)}</div>`;
            }
            if (def.chinese_example) {
                html += `<div class="def-example def-example-cn">💡 ${escapeHtml(def.chinese_example)}</div>`;
            }
            html += "</div>";
        }
    }

    // ── Inflected Forms (from ECDICT) ──
    const inflectedForms = parseExchange(data.exchange);
    if (inflectedForms.length > 0) {
        html += '<div class="section-title">🔄 词形变化</div>';
        html += '<div class="inflected-forms">';
        for (const f of inflectedForms) {
            html += `<span class="inflected-tag" data-word="${escapeHtml(f.form)}" title="${escapeHtml(f.label)}">${escapeHtml(f.form)}<small>${escapeHtml(f.label)}</small></span>`;
        }
        html += '</div>';
    }

    // ── Generic content sections driven by prompt config ──
    // Each enabled non-special field renders in config order (icon + label from config).
    if (cfg) {
        for (const f of cfg.fields) {
            if (!f.enabled || SPECIAL_FIELD_KEYS.includes(f.key)) continue;
            const val = data[f.key];
            if (!val) continue;
            html += `<div class="section-title">${escapeHtml(f.icon)} ${escapeHtml(f.label)}</div>`;
            if (f.type === "list") {
                html += '<div class="section-content">';
                const items = String(val).split(/[,，、;；\s]+/).filter(Boolean);
                for (const s of items) {
                    html += `<span class="synonym-tag" data-word="${escapeHtml(s)}">${escapeHtml(s)}</span>`;
                }
                html += "</div>";
            } else {
                html += `<div class="section-content">${escapeHtml(String(val))}</div>`;
            }
        }
    } else {
        // Fallback: old hardcoded sections when config not yet loaded
        if (data.memory_tips) {
            html += '<div class="section-title">🧠 记忆技巧</div>';
            html += `<div class="section-content">${escapeHtml(data.memory_tips)}</div>`;
        }
        if (data.synonyms) {
            html += '<div class="section-title">📌 近义词</div>';
            html += '<div class="section-content">';
            const syns = String(data.synonyms).split(/[,，、;；\s]+/).filter(Boolean);
            for (const s of syns) {
                html += `<span class="synonym-tag" data-word="${escapeHtml(s)}">${escapeHtml(s)}</span>`;
            }
            html += "</div>";
        }
        if (data.antonyms) {
            html += '<div class="section-title">🚫 反义词</div>';
            html += '<div class="section-content">';
            const ants = String(data.antonyms).split(/[,，、;；\s]+/).filter(Boolean);
            for (const a of ants) {
                html += `<span class="synonym-tag" data-word="${escapeHtml(a)}">${escapeHtml(a)}</span>`;
            }
            html += "</div>";
        }
        if (data.etymology) {
            html += '<div class="section-title">📚 词源</div>';
            html += `<div class="section-content">${escapeHtml(data.etymology)}</div>`;
        }
    }

    // ── Source indicator ──
    if (data._sources && data._sources.length > 0) {
        html += `<div class="source-indicator">数据来源: ${data._sources.map((s: string) => escapeHtml(s)).join(" + ")}</div>`;
    }

    // ── LLM error note ──
    if (data._llmError) {
        html += `<div class="llm-error-note">⚠️ LLM增强查询失败: ${escapeHtml(data._llmError)}</div>`;
    }

    // ── Loading more indicator ──
    if (isLoadingMore) {
        html += '<div class="loading-more">';
        html += '<div class="loading-more-spinner"></div>';
        html += '<span>正在获取更详细的释义...</span>';
        html += '</div>';
    }

    html += "</div>";
    return html;
}

// ============================================================
// History
// ============================================================
async function showHistory() {
    historyPanel.classList.remove("hidden");
    try {
        const entries = await GetHistory();
        if (!entries || entries.length === 0) {
            historyList.innerHTML = `
                <div class="empty-state">
                    <span class="empty-state-icon">📭</span>
                    <p>暂无查询历史</p>
                </div>`;
            return;
        }
        let html = "";
        for (const entry of entries) {
            html += `
                <div class="history-item" data-id="${escapeHtml(entry.id)}">
                    <div class="history-item-left">
                        <span class="history-word">${escapeHtml(entry.word)}</span>
                        <span class="history-time">${escapeHtml(formatTimestamp(entry.createdAt))}</span>
                    </div>
                    <div class="history-item-right">
                        <button class="history-btn view" data-id="${escapeHtml(entry.id)}" title="查看">👁️</button>
                        <button class="history-btn delete" data-id="${escapeHtml(entry.id)}" title="删除">🗑️</button>
                    </div>
                </div>`;
        }
        historyList.innerHTML = html;
    } catch (err) {
        console.error("Failed to load history:", err);
    }
}

async function viewHistoryEntry(id: string) {
    try {
        const entry = await GetHistoryEntry(id);
        if (!entry) return;
        modalTitle.textContent = `📖 ${entry.word}`;
        let parsed: any = null;
        try {
            parsed = JSON.parse(entry.result);
        } catch {
            modalBody.innerHTML = `<pre style="white-space:pre-wrap;color:var(--text-secondary);font-size:13px;">${escapeHtml(entry.result)}</pre>`;
            historyDetailModal.classList.remove("hidden");
            return;
        }
        modalBody.innerHTML = renderWordResult(parsed, false);
        historyDetailModal.classList.remove("hidden");
    } catch (err) {
        console.error(err);
    }
}

async function deleteHistoryEntry(id: string) {
    try {
        await DeleteHistory(id);
        await showHistory();
    } catch (err) {
        console.error(err);
    }
}

// ============================================================
// Settings
// ============================================================
async function showSettings() {
    settingsPanel.classList.remove("hidden");
    try {
        const config = await GetConfig();
        inputApiKey.value = config.apiKey || "";
        inputApiUrl.value = config.apiURL || "";
        inputModelName.value = config.modelName || "";
        inputShortcutKey.value = config.shortcutKey || "Ctrl+Alt+Q";
        autoStartToggle.checked = config.autoStart === "true";
        const display = document.getElementById("shortcut-display");
        if (display && config.shortcutKey) display.textContent = config.shortcutKey;

        // Update ECDICT status
        const ecdictStatus = document.getElementById("ecdict-status");
        if (ecdictStatus) {
            const available = await EcdictIsAvailable();
            if (available) {
                ecdictStatus.textContent = "✅ 已导入";
                ecdictStatus.className = "ecdict-status available";
            } else {
                ecdictStatus.textContent = "❌ 未导入";
                ecdictStatus.className = "ecdict-status unavailable";
            }
        }
    } catch (err) {
        console.error(err);
    }
}

async function saveConfig() {
    try {
        const shortcut = inputShortcutKey.value.trim() || "Ctrl+Alt+Q";
        await SaveConfig(inputApiKey.value, inputApiUrl.value, inputModelName.value, shortcut);
        showToast("设置已保存 ✅");
    } catch (err: any) {
        showError("保存失败: " + String(err));
    }
}

async function importEcdict() {
    if (!btnImportEcdict) return;
    btnImportEcdict.disabled = true;
    btnImportEcdict.textContent = "导入中...";

    try {
        // Pass empty string to let Go auto-search common locations
        // (exe dir, working dir, user data dir, .csv and .csv.gz)
        let csvPath = "";

        try {
            await ImportEcdict(csvPath);
        } catch (firstErr: any) {
            // Auto-search failed, ask user for path
            csvPath = prompt("未找到词典文件，请输入 ecdict.csv 或 ecdict.csv.gz 的完整路径:", "ECDICT/ecdict.csv") || "";
            if (!csvPath) {
                btnImportEcdict.disabled = false;
                btnImportEcdict.textContent = "导入 ECDICT 词典";
                return;
            }
            await ImportEcdict(csvPath);
        }
        ecdictAvailable = true;
        showToast("ECDICT 词典导入成功 ✅");

        // Update status
        const ecdictStatus = document.getElementById("ecdict-status");
        if (ecdictStatus) {
            ecdictStatus.textContent = "✅ 已导入";
            ecdictStatus.className = "ecdict-status available";
        }
    } catch (err: any) {
        showError("导入失败: " + String(err));
    } finally {
        btnImportEcdict.disabled = false;
        btnImportEcdict.textContent = "导入 ECDICT 词典";
    }
}

// ============================================================
// Helpers
// ============================================================
function showError(msg: string) {
    errorText.textContent = msg;
    errorEl.classList.remove("hidden");
}

function hideError() {
    errorEl.classList.add("hidden");
}

function escapeHtml(str: string): string {
    if (!str) return "";
    return String(str)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;");
}

let toastTimer: ReturnType<typeof setTimeout>;

/**
 * Format a timestamp (either Unix seconds number or date string) to a readable format.
 * Handles both the old "2006-01-02 15:04:05" string format and the new Unix timestamp format.
 */
function formatTimestamp(ts: string | number): string {
    if (!ts) return "";
    // If it's already a formatted date string (contains '-')
    if (typeof ts === 'string' && ts.includes('-')) {
        return ts;
    }
    // Unix timestamp (seconds)
    const num = typeof ts === 'number' ? ts : parseInt(ts, 10);
    if (isNaN(num)) return String(ts);
    const date = new Date(num * 1000);
    if (isNaN(date.getTime())) return String(ts);
    const y = date.getFullYear();
    const m = String(date.getMonth() + 1).padStart(2, '0');
    const d = String(date.getDate()).padStart(2, '0');
    const h = String(date.getHours()).padStart(2, '0');
    const min = String(date.getMinutes()).padStart(2, '0');
    const sec = String(date.getSeconds()).padStart(2, '0');
    return `${y}-${m}-${d} ${h}:${min}:${sec}`;
}
function showToast(msg: string) {
    let toast = document.getElementById("toast") as HTMLDivElement;
    if (!toast) {
        toast = document.createElement("div");
        toast.id = "toast";
        toast.className = "toast";
        document.body.appendChild(toast);
    }
    toast.textContent = msg;
    toast.classList.add("is-visible");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toast.classList.remove("is-visible"), 3000);
}

// ============================================================
// Event Listeners
// ============================================================
btnSearch.addEventListener("click", () => {
    const word = searchInput.value;
    doSearch(word);
});

// Clear button: clear input, hide results, focus input
btnClearSearch.addEventListener("click", () => {
    searchInput.value = "";
    btnClearSearch.classList.add("hidden");
    resultEl.classList.add("hidden");
    hideError();
    searchInput.focus();
});

// Show/hide clear button based on input content
searchInput.addEventListener("input", () => {
    if (searchInput.value.length > 0) {
        btnClearSearch.classList.remove("hidden");
    } else {
        btnClearSearch.classList.add("hidden");
    }
});

searchInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
        const word = searchInput.value;
        doSearch(word);
    }
});

btnHistory.addEventListener("click", showHistory);
btnCloseHistory.addEventListener("click", () => historyPanel.classList.add("hidden"));

btnSettings.addEventListener("click", showSettings);
btnCloseSettings.addEventListener("click", () => settingsPanel.classList.add("hidden"));

btnSaveConfig.addEventListener("click", saveConfig);

// Shortcut tab save button
const btnSaveConfigShortcut = document.getElementById("btn-save-config-shortcut") as HTMLButtonElement;
btnSaveConfigShortcut.addEventListener("click", saveConfig);

// Settings tab switching
const settingsTabs = document.querySelectorAll(".settings-tab");
const settingsTabPages = document.querySelectorAll(".settings-tab-page");

settingsTabs.forEach(tab => {
    tab.addEventListener("click", () => {
        const targetTab = (tab as HTMLElement).dataset.tab;
        settingsTabs.forEach(t => t.classList.remove("active"));
        settingsTabPages.forEach(p => p.classList.remove("active"));
        tab.classList.add("active");
        const page = document.getElementById(`settings-tab-${targetTab}`);
        if (page) page.classList.add("active");
    });
});

if (btnImportEcdict) {
    btnImportEcdict.addEventListener("click", importEcdict);
}

// Toggle API key visibility
btnToggleKey.addEventListener("click", () => {
    if (inputApiKey.type === "password") {
        inputApiKey.type = "text";
        btnToggleKey.textContent = "🙈";
    } else {
        inputApiKey.type = "password";
        btnToggleKey.textContent = "👁️";
    }
});

// Auto-start toggle
autoStartToggle.addEventListener("change", async () => {
    try {
        await SetAutoStart(autoStartToggle.checked);
        showToast(autoStartToggle.checked ? "Auto-start enabled ✅" : "Auto-start disabled");
    } catch (err: any) {
        autoStartToggle.checked = !autoStartToggle.checked;
        showError("Failed to set auto-start: " + String(err));
    }
});

// Shortcut key recording
let isRecording = false;

function startRecording() {
    isRecording = true;
    inputShortcutKey.value = "请按下快捷键组合...";
    inputShortcutKey.classList.add("recording");
    btnRecordShortcut.classList.add("recording");
    btnRecordShortcut.textContent = "录制中";
    inputShortcutKey.focus();
}

function stopRecording() {
    isRecording = false;
    inputShortcutKey.classList.remove("recording");
    btnRecordShortcut.classList.remove("recording");
    btnRecordShortcut.textContent = "录制";
}

btnRecordShortcut.addEventListener("click", () => {
    if (isRecording) {
        stopRecording();
        inputShortcutKey.value = "";
    } else {
        startRecording();
    }
});

inputShortcutKey.addEventListener("click", () => {
    if (!isRecording) startRecording();
});

inputShortcutKey.addEventListener("keydown", (e: KeyboardEvent) => {
    if (!isRecording) return;
    e.preventDefault();
    e.stopPropagation();

    const key = e.key;
    if (["Control", "Alt", "Shift", "Meta", "Super"].includes(key)) return;

    const parts: string[] = [];
    if (e.ctrlKey) parts.push("Ctrl");
    if (e.altKey) parts.push("Alt");
    if (e.shiftKey) parts.push("Shift");
    if (e.metaKey) parts.push("Super");

    let keyName = key;
    if (key === " ") keyName = "Space";
    else if (key.length === 1) keyName = key.toUpperCase();
    else if (key.startsWith("F") && /^F\d+$/.test(key)) keyName = key;
    else if (key === "Escape") { stopRecording(); return; }
    else if (key === "Backspace") { stopRecording(); inputShortcutKey.value = ""; return; }
    else keyName = key;

    parts.push(keyName);
    const accelerator = parts.join("+");

    if (parts.length >= 2) {
        inputShortcutKey.value = accelerator;
        stopRecording();
    }
});

inputShortcutKey.addEventListener("keyup", (e: KeyboardEvent) => {
    if (isRecording) {
        e.preventDefault();
        e.stopPropagation();
    }
});

// Esc key to hide window
document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && !isRecording) {
        if (promptModal && !promptModal.classList.contains("hidden")) {
            closePromptModal();
            return;
        }
        if (!historyDetailModal.classList.contains("hidden")) {
            historyDetailModal.classList.add("hidden");
            return;
        }
        if (!settingsPanel.classList.contains("hidden")) {
            settingsPanel.classList.add("hidden");
            return;
        }
        if (!historyPanel.classList.contains("hidden")) {
            historyPanel.classList.add("hidden");
            return;
        }
        // If search input has text, clear it first before hiding
        if (searchInput.value.trim() !== "") {
            searchInput.value = "";
            btnClearSearch.classList.add("hidden");
            resultEl.classList.add("hidden");
            searchInput.focus();
            return;
        }
        Events.Emit("hide-window");
    }
});

btnClearHistory.addEventListener("click", async () => {
    if (confirm("确定要清空所有查询历史吗？")) {
        try {
            await ClearHistory();
            await showHistory();
        } catch (err) {
            console.error(err);
        }
    }
});

btnCloseModal.addEventListener("click", () => {
    historyDetailModal.classList.add("hidden");
});

historyDetailModal.addEventListener("click", (e) => {
    if (e.target === historyDetailModal) {
        historyDetailModal.classList.add("hidden");
    }
});

// Delegated events for history list
historyList.addEventListener("click", (e) => {
    const target = e.target as HTMLElement;
    const viewBtn = target.closest(".history-btn.view") as HTMLElement;
    const deleteBtn = target.closest(".history-btn.delete") as HTMLElement;

    if (viewBtn) {
        viewHistoryEntry(viewBtn.dataset.id!);
    } else if (deleteBtn) {
        deleteHistoryEntry(deleteBtn.dataset.id!);
    } else {
        const item = target.closest(".history-item") as HTMLElement;
        if (item && item.dataset.id) {
            viewHistoryEntry(item.dataset.id);
        }
    }
});

// Click synonym/inflected tags to search
resultEl.addEventListener("click", (e) => {
    const target = e.target as HTMLElement;
    const tag = target.closest(".synonym-tag, .inflected-tag") as HTMLElement;
    if (tag && tag.dataset.word) {
        doSearch(tag.dataset.word);
    }
});
modalBody.addEventListener("click", (e) => {
    const target = e.target as HTMLElement;
    const tag = target.closest(".synonym-tag, .inflected-tag") as HTMLElement;
    if (tag && tag.dataset.word) {
        historyDetailModal.classList.add("hidden");
        doSearch(tag.dataset.word);
    }
});

// Listen for clipboard-english-detected event from Go
let clipboardDebounce: ReturnType<typeof setTimeout>;
let lastUserTyping = 0; // timestamp of last user keystroke in search input

searchInput.addEventListener("keydown", () => { lastUserTyping = Date.now(); });

Events.On("clipboard-english-detected", (event: any) => {
    const word = event.data;
    console.log(`[CLIPBOARD-DEBUG] Event received: word="${word}", isSearching=${isSearching}, currentWord="${currentWord}", lastUserTyping=${Date.now() - lastUserTyping}ms ago`);
    if (!word) return;
    // Skip if the user has typed in the search input in the last 2 seconds
    // (they're actively entering a word manually — don't overwrite)
    if (Date.now() - lastUserTyping < 2000) {
        console.log(`[CLIPBOARD-DEBUG] SKIPPED: user typed recently`);
        return;
    }
    // Skip if we're already searching this exact word
    if (currentWord === word && isSearching) {
        console.log(`[CLIPBOARD-DEBUG] SKIPPED: already searching this word`);
        return;
    }
    console.log(`[CLIPBOARD-DEBUG] Will search for "${word}" in 300ms`);
    // Debounce: wait 300ms before acting on clipboard change
    clearTimeout(clipboardDebounce);
    clipboardDebounce = setTimeout(() => {
        doSearch(word);
    }, 300);
});

// Focus search input on load
searchInput.focus();

// ============================================================
// Sync - Multi-device sync with WeChat QR code login
// ============================================================
const inputSyncServerAddr = document.getElementById("sync-server-addr") as HTMLInputElement;
const inputSyncUserToken = document.getElementById("sync-user-token") as HTMLInputElement;
const inputSyncAutoSync = document.getElementById("sync-auto-sync") as HTMLInputElement;
const btnToggleSyncToken = document.getElementById("btn-toggle-sync-token") as HTMLButtonElement;
const btnSyncTest = document.getElementById("btn-sync-test") as HTMLButtonElement;
const btnSyncPush = document.getElementById("btn-sync-push") as HTMLButtonElement;
const btnSyncPull = document.getElementById("btn-sync-pull") as HTMLButtonElement;
const btnSaveSyncConfig = document.getElementById("btn-save-sync-config") as HTMLButtonElement;
const btnSyncQrCode = document.getElementById("btn-sync-qrcode") as HTMLButtonElement;
const qrCodeDisplay = document.getElementById("qr-code-display") as HTMLDivElement;
const qrCodeImage = document.getElementById("qr-code-image") as HTMLImageElement;
const qrPairingCode = document.getElementById("qr-pairing-code") as HTMLDivElement;
const qrStatusText = document.getElementById("qr-status-text") as HTMLDivElement;
const syncTokenSection = document.getElementById("sync-token-section") as HTMLDivElement;

let qrPollTimer: ReturnType<typeof setInterval> | null = null;
let currentQrScene = "";

async function loadSyncConfig() {
    try {
        const config = await GetSyncConfig() as any;
        if (config) {
            inputSyncServerAddr.value = config.syncAddr || "";
            inputSyncUserToken.value = config.syncToken || "";
            inputSyncAutoSync.checked = config.autoSync === "true";
            // Show/hide token section based on whether token exists
            updateSyncUI(config.syncToken);
        }
    } catch (err) {
        console.error("Failed to load sync config:", err);
    }
}

function updateSyncUI(token?: string) {
    const hasToken = !!(token || inputSyncUserToken.value);
    if (hasToken) {
        syncTokenSection.classList.remove("hidden");
    } else {
        syncTokenSection.classList.add("hidden");
    }
}

async function testConnection() {
    try {
        await SaveSyncConfig(inputSyncServerAddr.value, inputSyncUserToken.value, inputSyncAutoSync.checked);
        btnSyncTest.disabled = true;
        btnSyncTest.textContent = "Testing...";
        const result = await TestConnection();
        showToast(result);
    } catch (err: any) {
        showError("Connection failed: " + String(err));
    } finally {
        btnSyncTest.disabled = false;
        btnSyncTest.textContent = "🔗 Test Connection";
    }
}

async function requestQrCode() {
    // Stop any existing poll
    stopQrPoll();

    try {
        await SaveSyncConfig(inputSyncServerAddr.value, inputSyncUserToken.value, inputSyncAutoSync.checked);
        btnSyncQrCode.disabled = true;
        btnSyncQrCode.textContent = "Generating...";
        qrStatusText.textContent = "Requesting QR code...";

        const result = await RequestQrCode() as any;

        currentQrScene = result.scene;
        const expiresIn = result.expiresIn || 300;

        if (result.qrcode) {
            // WeChat QR code image available — display it
            qrCodeImage.src = result.qrcode;
            qrCodeImage.classList.remove("hidden");
            qrPairingCode.classList.add("hidden");
            qrCodeDisplay.classList.remove("hidden");
            qrStatusText.textContent = `Scan with WeChat (expires in ${Math.ceil(expiresIn / 60)} min)`;
        } else {
            // Fallback: show pairing code
            qrCodeImage.classList.add("hidden");
            qrPairingCode.textContent = result.scene;
            qrPairingCode.classList.remove("hidden");
            qrCodeDisplay.classList.remove("hidden");
            qrStatusText.textContent = `Enter this code in the mini program (expires in ${Math.ceil(expiresIn / 60)} min)`;
        }

        // Start polling for scan status
        startQrPoll();

    } catch (err: any) {
        showError("QR code failed: " + String(err));
        qrStatusText.textContent = "Failed to generate QR code";
    } finally {
        btnSyncQrCode.disabled = false;
        btnSyncQrCode.textContent = "📱 Generate QR Code";
    }
}

function startQrPoll() {
    stopQrPoll();
    qrPollTimer = setInterval(async () => {
        try {
            const result = await PollQrCodeStatus(currentQrScene) as any;

            if (result.status === "scanned" && result.token) {
                // Login complete!
                stopQrPoll();
                inputSyncUserToken.value = result.token;
                updateSyncUI(result.token);
                qrCodeDisplay.classList.add("hidden");
                qrStatusText.textContent = "✅ Login successful! Token saved.";
                showToast("WeChat login successful! ✅");
            } else if (result.status === "expired") {
                stopQrPoll();
                qrCodeDisplay.classList.add("hidden");
                qrStatusText.textContent = "QR code expired. Click to generate a new one.";
            } else {
                // Still pending
                qrStatusText.textContent = `Waiting for scan... (scene: ${currentQrScene})`;
            }
        } catch (err: any) {
            console.error("QR poll error:", err);
            stopQrPoll();
            qrStatusText.textContent = "Polling failed. Please try again.";
        }
    }, 3000); // Poll every 3 seconds
}

function stopQrPoll() {
    if (qrPollTimer) {
        clearInterval(qrPollTimer);
        qrPollTimer = null;
    }
}

async function syncPush() {
    try {
        await SaveSyncConfig(inputSyncServerAddr.value, inputSyncUserToken.value, inputSyncAutoSync.checked);
        btnSyncPush.disabled = true;
        btnSyncPush.textContent = "Pushing...";
        const result = await PushToServer();
        showToast(result);
    } catch (err: any) {
        showError("Push failed: " + String(err));
    } finally {
        btnSyncPush.disabled = false;
        btnSyncPush.textContent = "⬆️ Push to Server";
    }
}

async function syncPull() {
    try {
        await SaveSyncConfig(inputSyncServerAddr.value, inputSyncUserToken.value, inputSyncAutoSync.checked);
        btnSyncPull.disabled = true;
        btnSyncPull.textContent = "Pulling...";
        const result = await PullFromServer();
        showToast(result);
        if (!historyPanel.classList.contains("hidden")) {
            await showHistory();
        }
    } catch (err: any) {
        showError("Pull failed: " + String(err));
    } finally {
        btnSyncPull.disabled = false;
        btnSyncPull.textContent = "⬇️ Pull from Server";
    }
}

async function saveSyncConfig() {
    try {
        await SaveSyncConfig(inputSyncServerAddr.value, inputSyncUserToken.value, inputSyncAutoSync.checked);
        showToast("Sync settings saved ✅");
    } catch (err: any) {
        showError("Save failed: " + String(err));
    }
}

// Sync event listeners
btnSyncTest.addEventListener("click", testConnection);
btnSyncQrCode.addEventListener("click", requestQrCode);
btnSyncPush.addEventListener("click", syncPush);
btnSyncPull.addEventListener("click", syncPull);
btnSaveSyncConfig.addEventListener("click", saveSyncConfig);

// Toggle token visibility
if (btnToggleSyncToken) {
    btnToggleSyncToken.addEventListener("click", () => {
        if (inputSyncUserToken.type === "password") {
            inputSyncUserToken.type = "text";
            btnToggleSyncToken.textContent = "🙈";
        } else {
            inputSyncUserToken.type = "password";
            btnToggleSyncToken.textContent = "👁️";
        }
    });
}

// Load sync config when settings panel is shown
const origShowSettings = showSettings;
const _origShowSettingsBtn = btnSettings.onclick;
btnSettings.removeEventListener("click", _origShowSettingsBtn as EventListener);
btnSettings.addEventListener("click", async () => {
    await origShowSettings();
    await loadSyncConfig();
});

// ============================================================
// Prompt Settings Modal
// ============================================================
const promptModal = document.getElementById("prompt-modal") as HTMLDivElement;
const btnPromptSettings = document.getElementById("btn-prompt-settings") as HTMLButtonElement;
const btnClosePromptModal = document.getElementById("btn-close-prompt-modal") as HTMLButtonElement;
const promptSystemInput = document.getElementById("prompt-system") as HTMLTextAreaElement;
const promptFieldsList = document.getElementById("prompt-fields-list") as HTMLDivElement;
const btnAddField = document.getElementById("btn-add-field") as HTMLButtonElement;
const promptExtraInput = document.getElementById("prompt-extra") as HTMLTextAreaElement;
const promptTempRange = document.getElementById("prompt-temperature") as HTMLInputElement;
const promptTempVal = document.getElementById("prompt-temp-val") as HTMLSpanElement;
const promptMaxTokensInput = document.getElementById("prompt-max-tokens") as HTMLInputElement;
const promptPreviewWordInput = document.getElementById("prompt-preview-word") as HTMLInputElement;
const promptPreviewPre = document.getElementById("prompt-preview") as HTMLPreElement;
const promptTestWordInput = document.getElementById("prompt-test-word") as HTMLInputElement;
const btnPromptTest = document.getElementById("btn-prompt-test") as HTMLButtonElement;
const promptTestResult = document.getElementById("prompt-test-result") as HTMLDivElement;
const btnPromptReset = document.getElementById("btn-prompt-reset") as HTMLButtonElement;
const btnPromptSave = document.getElementById("btn-prompt-save") as HTMLButtonElement;

// Working copy of the config being edited in the modal
let editorConfig: PromptConfig | null = null;
let previewDebounce: ReturnType<typeof setTimeout>;

function typeLabel(t: string): string {
    if (t === "list") return "列表";
    if (t === "definitions") return "释义(结构)";
    return "文本";
}

function populateFromEditor() {
    if (!editorConfig) return;
    promptSystemInput.value = editorConfig.systemPrompt;
    promptExtraInput.value = editorConfig.extraRequirements;
    promptTempRange.value = String(editorConfig.temperature);
    promptTempVal.textContent = String(editorConfig.temperature);
    promptMaxTokensInput.value = String(editorConfig.maxTokens);
    renderPromptFields();
    updatePromptPreview();
}

function openPromptModal() {
    GetPromptConfig().then(json => {
        let parsed: PromptConfig | null = null;
        if (json) { try { parsed = JSON.parse(json); } catch { /* ignore */ } }
        editorConfig = parsed || defaultPromptConfig();
        editorConfig = JSON.parse(JSON.stringify(editorConfig)); // deep copy
        populateFromEditor();
        showPromptModal();
    }).catch(() => {
        editorConfig = defaultPromptConfig();
        populateFromEditor();
        showPromptModal();
    });
}

function showPromptModal() {
    promptModal.classList.remove("hidden");
    // Freeze the settings panel scrollbar while the modal is on top,
    // so only the modal's own scrollbar is visible (no stacking).
    settingsPanel.classList.add("behind-modal");
}

function closePromptModal() {
    promptModal.classList.add("hidden");
    settingsPanel.classList.remove("behind-modal");
}

function renderPromptFields() {
    if (!editorConfig) return;
    let html = "";
    for (const f of editorConfig.fields) {
        const isWord = f.key === "word";
        const disabledCls = f.enabled ? "" : " disabled";
        html += `<div class="prompt-field-card${disabledCls}" data-key="${escapeHtml(f.key)}">`;
        html += '<div class="prompt-field-top">';
        html += `<input class="pf-icon" value="${escapeHtml(f.icon)}" maxlength="6" data-prop="icon" title="图标"/>`;
        html += `<input class="pf-label" value="${escapeHtml(f.label)}" placeholder="显示名" data-prop="label"/>`;
        html += `<span class="pf-key${f.builtin ? " builtin-key" : ""}" title="${f.builtin ? "内置字段，不可删除" : "自定义字段"}">${f.builtin ? "🔒" : "🔑"} ${escapeHtml(f.key)}</span>`;
        if (f.builtin) {
            html += `<span class="pf-type">${escapeHtml(typeLabel(f.type))}</span>`;
        } else {
            html += `<select class="pf-type" data-prop="type" title="字段类型">`;
            html += `<option value="text" ${f.type === "text" ? "selected" : ""}>文本</option>`;
            html += `<option value="list" ${f.type === "list" ? "selected" : ""}>列表</option>`;
            html += `</select>`;
        }
        html += `<label class="pf-toggle"><input type="checkbox" data-prop="enabled" ${f.enabled ? "checked" : ""} ${isWord ? "disabled" : ""}/> 启用</label>`;
        html += `<button class="pf-delete" data-action="delete" ${f.builtin ? "disabled" : ""} title="${f.builtin ? "内置字段不可删除" : "删除字段"}">🗑️</button>`;
        html += "</div>";
        if (f.key === "definitions") {
            html += '<div class="pf-hint">内置结构：词性 / 中文释义 / 英文例句 / 中文翻译（不可自定义结构）</div>';
        } else if (isWord) {
            html += '<div class="pf-hint">固定字段：用于标识被查询的单词</div>';
        } else {
            html += `<input class="pf-desc" value="${escapeHtml(f.desc)}" placeholder="说明（同时作为字段要求发给 AI）" data-prop="desc"/>`;
        }
        html += "</div>";
    }
    promptFieldsList.innerHTML = html;
}

function schedulePreview() {
    clearTimeout(previewDebounce);
    previewDebounce = setTimeout(updatePromptPreview, 200);
}

function updatePromptPreview() {
    if (!editorConfig) return;
    const word = promptPreviewWordInput.value.trim() || "example";
    promptPreviewPre.textContent = buildPromptPreview(editorConfig, word);
}

async function testPromptAction() {
    if (!editorConfig) return;
    const word = promptTestWordInput.value.trim();
    if (!word) { showToast("请输入测试单词"); return; }
    btnPromptTest.disabled = true;
    btnPromptTest.textContent = "测试中...";
    promptTestResult.className = "prompt-test-result loading";
    promptTestResult.textContent = "正在调用 LLM，请稍候...";
    try {
        const result = await TestPrompt(word, JSON.stringify(editorConfig));
        let pretty = result;
        try { pretty = JSON.stringify(JSON.parse(result), null, 2); } catch { /* show raw */ }
        promptTestResult.className = "prompt-test-result ok";
        promptTestResult.textContent = pretty;
    } catch (err: any) {
        promptTestResult.className = "prompt-test-result err";
        promptTestResult.textContent = "❌ " + String(err);
    } finally {
        btnPromptTest.disabled = false;
        btnPromptTest.textContent = "测试";
    }
}

async function savePromptConfigAction() {
    if (!editorConfig) return;
    btnPromptSave.disabled = true;
    const origText = btnPromptSave.textContent;
    btnPromptSave.textContent = "保存中...";
    try {
        await SavePromptConfig(JSON.stringify(editorConfig));
        promptConfig = JSON.parse(JSON.stringify(editorConfig));
        showToast("提示词设置已保存 ✅");
    } catch (err: any) {
        showError("保存失败: " + String(err));
    } finally {
        btnPromptSave.disabled = false;
        btnPromptSave.textContent = origText;
    }
}

btnPromptSettings.addEventListener("click", openPromptModal);
btnClosePromptModal.addEventListener("click", () => closePromptModal());
promptModal.addEventListener("click", (e) => {
    if (e.target === promptModal) closePromptModal();
});

btnAddField.addEventListener("click", () => {
    if (!editorConfig) return;
    const key = prompt("请输入字段标识（英文/数字/下划线，如 collocations）：", "custom_field");
    if (!key) return;
    const k = key.trim();
    if (!/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(k)) { showToast("字段标识只能用英文字母、数字和下划线，且以字母开头"); return; }
    if (SPECIAL_FIELD_KEYS.includes(k)) { showToast("该字段标识为保留字，请换一个"); return; }
    if (editorConfig.fields.some(f => f.key === k)) { showToast("字段标识已存在"); return; }
    editorConfig.fields.push({ key: k, label: k, icon: "🔹", type: "text", desc: "", enabled: true, builtin: false });
    renderPromptFields();
    schedulePreview();
});

promptFieldsList.addEventListener("input", (e) => {
    const target = e.target as HTMLElement;
    const card = target.closest(".prompt-field-card") as HTMLElement;
    if (!card || !editorConfig) return;
    const f = editorConfig.fields.find(x => x.key === card.dataset.key);
    if (!f) return;
    const prop = (target as any).dataset.prop;
    if (prop === "icon") f.icon = (target as HTMLInputElement).value;
    else if (prop === "label") f.label = (target as HTMLInputElement).value;
    else if (prop === "desc") f.desc = (target as HTMLInputElement).value;
    if (prop) schedulePreview();
});

promptFieldsList.addEventListener("change", (e) => {
    const target = e.target as HTMLElement;
    const card = target.closest(".prompt-field-card") as HTMLElement;
    if (!card || !editorConfig) return;
    const f = editorConfig.fields.find(x => x.key === card.dataset.key);
    if (!f) return;
    const prop = (target as any).dataset.prop;
    if (prop === "enabled") {
        f.enabled = (target as HTMLInputElement).checked;
        card.classList.toggle("disabled", !f.enabled);
    } else if (prop === "type") {
        f.type = (target as HTMLSelectElement).value as PromptField["type"];
    }
    schedulePreview();
});

promptFieldsList.addEventListener("click", (e) => {
    const target = e.target as HTMLElement;
    const delBtn = target.closest(".pf-delete") as HTMLButtonElement;
    if (!delBtn || delBtn.disabled || !editorConfig) return;
    const card = delBtn.closest(".prompt-field-card") as HTMLElement;
    const key = card.dataset.key!;
    if (!confirm(`确定删除字段 “${key}” 吗？`)) return;
    editorConfig.fields = editorConfig.fields.filter(f => f.key !== key);
    renderPromptFields();
    schedulePreview();
});

promptSystemInput.addEventListener("input", () => { if (editorConfig) editorConfig.systemPrompt = promptSystemInput.value; schedulePreview(); });
promptExtraInput.addEventListener("input", () => { if (editorConfig) editorConfig.extraRequirements = promptExtraInput.value; schedulePreview(); });
promptTempRange.addEventListener("input", () => { const v = parseFloat(promptTempRange.value); if (editorConfig) editorConfig.temperature = v; promptTempVal.textContent = promptTempRange.value; });
promptMaxTokensInput.addEventListener("input", () => { const v = parseInt(promptMaxTokensInput.value, 10) || 2000; if (editorConfig) editorConfig.maxTokens = v; });
promptPreviewWordInput.addEventListener("input", schedulePreview);
btnPromptTest.addEventListener("click", testPromptAction);
btnPromptSave.addEventListener("click", savePromptConfigAction);
btnPromptReset.addEventListener("click", () => {
    if (!confirm("确定恢复全部提示词设置为默认值吗？自定义字段将被移除。")) return;
    editorConfig = defaultPromptConfig();
    populateFromEditor();
});

// ============================================================
// Debug Tab - Cache stats & Prompt structure inspection
// ============================================================
const btnDebugRefresh = document.getElementById("btn-debug-refresh") as HTMLButtonElement;
const debugCacheStats = document.getElementById("debug-cache-stats") as HTMLPreElement;
const btnDebugPrompt = document.getElementById("btn-debug-prompt") as HTMLButtonElement;
const debugPromptWord = document.getElementById("debug-prompt-word") as HTMLInputElement;
const debugPromptInfo = document.getElementById("debug-prompt-info") as HTMLPreElement;

async function refreshCacheStats() {
    try {
        const stats = await GetCacheStats() as any;
        debugCacheStats.textContent = JSON.stringify(stats, null, 2);
    } catch (err: any) {
        debugCacheStats.textContent = "Error: " + String(err);
    }
}

async function inspectPrompt() {
    const word = debugPromptWord.value.trim() || "example";
    try {
        const info = await GetPromptDebugInfo(word) as any;
        // Format nicely
        const formatted = Object.entries(info)
            .map(([k, v]) => {
                if (k.endsWith("Len") || k === "memoryCacheSize") {
                    return `${k}: ${v}`;
                }
                return `--- ${k} ---\n${v}`;
            })
            .join("\n\n");
        debugPromptInfo.textContent = formatted;
    } catch (err: any) {
        debugPromptInfo.textContent = "Error: " + String(err);
    }
}

btnDebugRefresh.addEventListener("click", refreshCacheStats);
btnDebugPrompt.addEventListener("click", inspectPrompt);

// Auto-load stats when debug tab is shown
const origSettingsTabs = document.querySelectorAll(".settings-tab");
origSettingsTabs.forEach(tab => {
    tab.addEventListener("click", () => {
        if ((tab as HTMLElement).dataset.tab === "debug") {
            refreshCacheStats();
        }
    });
});
