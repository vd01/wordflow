import { Events } from "@wailsio/runtime";
import { LookupWordFast, LookupWordLLM, GetConfig, SaveConfig, ReadClipboard, IsEnglishText } from "../bindings/wordwise/dictservice.js";
import { EcdictIsAvailable, ImportEcdict } from "../bindings/wordwise/ecdictservice.js";
import { GetHistory, AddHistory, DeleteHistory, ClearHistory, GetHistoryEntry } from "../bindings/wordwise/historyservice.js";
import { GetSyncConfig, SaveSyncConfig, TestConnection, CreateUser, PushToServer, PullFromServer } from "../bindings/wordwise/syncservice.js";

// ============================================================
// DOM Elements
// ============================================================
const searchInput = document.getElementById("search-input") as HTMLInputElement;
const btnSearch = document.getElementById("btn-search") as HTMLButtonElement;
const btnPaste = document.getElementById("btn-paste") as HTMLButtonElement;
const btnHistory = document.getElementById("btn-history") as HTMLButtonElement;
const btnSettings = document.getElementById("btn-settings") as HTMLButtonElement;
const btnMinimize = document.getElementById("btn-minimize") as HTMLButtonElement;
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
        console.log("[WordWise] ECDICT offline dictionary is available");
    } else {
        console.log("[WordWise] ECDICT offline dictionary not found - run import first");
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
    if (isSearching) return;

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
        // ── Phase 1: ECDICT fast lookup (~10ms) ──
        const ecdictResult = await LookupWordFast(word);
        if (currentWord !== word) return; // Stale query

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
                console.error("[WordWise] Failed to parse ECDICT result:", e);
            }
        }

        // ── Phase 2: LLM enrichment (slow, ~2-10s) ──
        try {
            const llmResult = await LookupWordLLM(word);
            if (currentWord !== word) return; // Stale query

            if (llmResult) {
                let parsed: any = null;
                let rawResult = llmResult.trim();

                // Robust JSON parsing
                try {
                    parsed = JSON.parse(rawResult);
                } catch {
                    const jsonMatch = rawResult.match(/\{[\s\S]*\}/);
                    if (jsonMatch) {
                        try {
                            parsed = JSON.parse(jsonMatch[0]);
                        } catch {
                            let cleaned = jsonMatch[0].replace(/[\x00-\x1f\x7f]/g, (ch) => {
                                if (ch === '\n' || ch === '\r' || ch === '\t') return ch;
                                return '';
                            });
                            try {
                                parsed = JSON.parse(cleaned);
                            } catch {
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

        // Save to history (merged result)
        AddHistory(word, JSON.stringify(mergedData)).catch(console.error);

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
    const merged: any = { word: llm?.word || ecdict?.word || "" };

    // Phonetic: prefer ECDICT if available (it's from a real dictionary)
    // But LLM may provide IPA format which is better
    if (ecdict?.phonetic && ecdict.phonetic.startsWith("/")) {
        merged.phonetic = ecdict.phonetic;
    } else if (llm?.phonetic) {
        merged.phonetic = llm.phonetic;
    } else {
        merged.phonetic = ecdict?.phonetic || llm?.phonetic || "";
    }

    // Pronunciation: only from LLM
    merged.pronunciation = llm?.pronunciation || "";

    // Translation: ECDICT Chinese translation is authoritative
    merged.translation = ecdict?.translation || llm?.translation || "";

    // Definitions: LLM provides structured definitions with examples
    // ECDICT provides English definitions (less structured)
    if (llm?.definitions && Array.isArray(llm.definitions) && llm.definitions.length > 0) {
        merged.definitions = llm.definitions;
    } else if (ecdict?.definition) {
        // Fallback: parse ECDICT English definitions
        merged.definitions = parseEcdictDefinitions(ecdict.definition, ecdict?.pos);
    } else {
        merged.definitions = [];
    }

    // Memory tips: only from LLM
    merged.memory_tips = llm?.memory_tips || "";

    // Synonyms: combine both
    const syns = new Set<string>();
    if (ecdict?.synonyms) String(ecdict.synonyms).split(/[,，、;；\s]+/).filter(Boolean).forEach(s => syns.add(s));
    if (llm?.synonyms) String(llm.synonyms).split(/[,，、;；\s]+/).filter(Boolean).forEach(s => syns.add(s));
    merged.synonyms = [...syns].join(", ");

    // Antonyms: from LLM
    merged.antonyms = llm?.antonyms || "";

    // Etymology: from LLM
    merged.etymology = llm?.etymology || "";

    // ECDICT-specific fields
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
    let html = '<div class="result-card">';

    // Word header
    html += '<div class="word-header">';
    html += `<span class="word-text">${escapeHtml(data.word || "")}</span>`;
    if (data.phonetic) {
        html += `<span class="word-phonetic">${escapeHtml(data.phonetic)}</span>`;
    }
    if (data.pronunciation) {
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

    // ── Chinese Translation (from ECDICT - fast) ──
    if (data.translation) {
        html += '<div class="translation-block">';
        // Translation may have multiple lines
        const transLines = String(data.translation).split("\n").filter(Boolean);
        for (const line of transLines) {
            html += `<div class="translation-line">${escapeHtml(line)}</div>`;
        }
        html += '</div>';
    }

    // ── Definitions (from LLM with examples, or ECDICT fallback) ──
    if (data.definitions && Array.isArray(data.definitions)) {
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

    // ── Memory tips (from LLM) ──
    if (data.memory_tips) {
        html += '<div class="section-title">🧠 记忆技巧</div>';
        html += `<div class="section-content">${escapeHtml(data.memory_tips)}</div>`;
    }

    // ── Synonyms ──
    if (data.synonyms) {
        html += '<div class="section-title">📌 近义词</div>';
        html += '<div class="section-content">';
        const syns = String(data.synonyms).split(/[,，、;；\s]+/).filter(Boolean);
        for (const s of syns) {
            html += `<span class="synonym-tag" data-word="${escapeHtml(s)}">${escapeHtml(s)}</span>`;
        }
        html += "</div>";
    }

    // ── Antonyms ──
    if (data.antonyms) {
        html += '<div class="section-title">🚫 反义词</div>';
        html += '<div class="section-content">';
        const ants = String(data.antonyms).split(/[,，、;；\s]+/).filter(Boolean);
        for (const a of ants) {
            html += `<span class="synonym-tag" data-word="${escapeHtml(a)}">${escapeHtml(a)}</span>`;
        }
        html += "</div>";
    }

    // ── Etymology (from LLM) ──
    if (data.etymology) {
        html += '<div class="section-title">📚 词源</div>';
        html += `<div class="section-content">${escapeHtml(data.etymology)}</div>`;
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
// Clipboard
// ============================================================
async function pasteAndSearch() {
    try {
        const text = await ReadClipboard();
        if (text) {
            searchInput.value = text;
            if (await IsEnglishText(text)) {
                doSearch(text);
            } else {
                showToast("剪贴板内容不是英语单词/短语");
            }
        } else {
            showToast("剪贴板为空");
        }
    } catch (err) {
        console.error(err);
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
searchInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
        const word = searchInput.value;
        doSearch(word);
    }
});

btnPaste.addEventListener("click", pasteAndSearch);

btnHistory.addEventListener("click", showHistory);
btnCloseHistory.addEventListener("click", () => historyPanel.classList.add("hidden"));

btnSettings.addEventListener("click", showSettings);
btnCloseSettings.addEventListener("click", () => settingsPanel.classList.add("hidden"));

btnSaveConfig.addEventListener("click", saveConfig);

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
        Events.Emit("hide-window");
    }
});

btnMinimize.addEventListener("click", () => {
    Events.Emit("hide-window");
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
Events.On("clipboard-english-detected", (event: any) => {
    const word = event.data;
    if (word) {
        doSearch(word);
    }
});

// Focus search input on load
searchInput.focus();

// ============================================================
// Sync - 多设备同步
// ============================================================
const inputSyncServerAddr = document.getElementById("sync-server-addr") as HTMLInputElement;
const inputSyncUserToken = document.getElementById("sync-user-token") as HTMLInputElement;
const inputSyncAutoSync = document.getElementById("sync-auto-sync") as HTMLInputElement;
const btnToggleSyncToken = document.getElementById("btn-toggle-sync-token") as HTMLButtonElement;
const btnSyncTest = document.getElementById("btn-sync-test") as HTMLButtonElement;
const btnSyncCreateUser = document.getElementById("btn-sync-create-user") as HTMLButtonElement;
const btnSyncPush = document.getElementById("btn-sync-push") as HTMLButtonElement;
const btnSyncPull = document.getElementById("btn-sync-pull") as HTMLButtonElement;
const btnSaveSyncConfig = document.getElementById("btn-save-sync-config") as HTMLButtonElement;

async function loadSyncConfig() {
    try {
        const config = await GetSyncConfig() as any;
        if (config) {
            inputSyncServerAddr.value = config.syncAddr || "";
            inputSyncUserToken.value = config.syncToken || "";
            inputSyncAutoSync.checked = config.autoSync === "true";
        }
    } catch (err) {
        console.error("Failed to load sync config:", err);
    }
}

async function testConnection() {
    try {
        await SaveSyncConfig(inputSyncServerAddr.value, inputSyncUserToken.value, inputSyncAutoSync.checked);
        btnSyncTest.disabled = true;
        btnSyncTest.textContent = "测试中...";
        const result = await TestConnection();
        showToast(result);
    } catch (err: any) {
        showError("连接失败: " + String(err));
    } finally {
        btnSyncTest.disabled = false;
        btnSyncTest.textContent = "🔗 测试连接";
    }
}

async function createUser() {
    try {
        await SaveSyncConfig(inputSyncServerAddr.value, "", inputSyncAutoSync.checked);
        btnSyncCreateUser.disabled = true;
        btnSyncCreateUser.textContent = "获取中...";
        const token = await CreateUser();
        inputSyncUserToken.value = token;
        showToast(`Token 获取成功！请妥善保管 ✅`);
    } catch (err: any) {
        showError("获取Token失败: " + String(err));
    } finally {
        btnSyncCreateUser.disabled = false;
        btnSyncCreateUser.textContent = "🆕 获取Token";
    }
}

async function syncPush() {
    try {
        await SaveSyncConfig(inputSyncServerAddr.value, inputSyncUserToken.value, inputSyncAutoSync.checked);
        btnSyncPush.disabled = true;
        btnSyncPush.textContent = "推送中...";
        const result = await PushToServer();
        showToast(result);
    } catch (err: any) {
        showError("推送失败: " + String(err));
    } finally {
        btnSyncPush.disabled = false;
        btnSyncPush.textContent = "⬆️ 推送到服务器";
    }
}

async function syncPull() {
    try {
        await SaveSyncConfig(inputSyncServerAddr.value, inputSyncUserToken.value, inputSyncAutoSync.checked);
        btnSyncPull.disabled = true;
        btnSyncPull.textContent = "拉取中...";
        const result = await PullFromServer();
        showToast(result);
        if (!historyPanel.classList.contains("hidden")) {
            await showHistory();
        }
    } catch (err: any) {
        showError("拉取失败: " + String(err));
    } finally {
        btnSyncPull.disabled = false;
        btnSyncPull.textContent = "⬇️ 从服务器拉取";
    }
}

async function saveSyncConfig() {
    try {
        await SaveSyncConfig(inputSyncServerAddr.value, inputSyncUserToken.value, inputSyncAutoSync.checked);
        showToast("同步设置已保存 ✅");
    } catch (err: any) {
        showError("保存失败: " + String(err));
    }
}

// Sync event listeners
btnSyncTest.addEventListener("click", testConnection);
btnSyncCreateUser.addEventListener("click", createUser);
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
