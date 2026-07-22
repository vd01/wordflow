import { Events } from "@wailsio/runtime";
import { LookupWord, GetConfig, SaveConfig, ReadClipboard, IsEnglishText } from "../bindings/wordwise/dictservice.js";
import { GetHistory, AddHistory, DeleteHistory, ClearHistory, GetHistoryEntry } from "../bindings/wordwise/historyservice.js";

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

// ============================================================
// Search
// ============================================================
async function doSearch(word: string) {
    word = word.trim();
    if (!word) {
        showToast("请输入要查询的单词");
        return;
    }
    if (isSearching) return;

    isSearching = true;
    searchInput.value = word;
    hideError();
    resultEl.classList.add("hidden");
    loadingEl.classList.remove("hidden");

    try {
        const result = await LookupWord(word);
        loadingEl.classList.add("hidden");

        // Try parse JSON with robust error handling
        let parsed: any = null;
        let rawResult = result.trim();

        // Step 1: direct parse
        try {
            parsed = JSON.parse(rawResult);
        } catch {
            // Step 2: extract JSON object from response
            const jsonMatch = rawResult.match(/\{[\s\S]*\}/);
            if (jsonMatch) {
                try {
                    parsed = JSON.parse(jsonMatch[0]);
                } catch {
                    // Step 3: try fixing common issues - remove control chars
                    let cleaned = jsonMatch[0].replace(/[\x00-\x1f\x7f]/g, (ch) => {
                        if (ch === '\n' || ch === '\r' || ch === '\t') return ch;
                        return '';
                    });
                    try {
                        parsed = JSON.parse(cleaned);
                    } catch {
                        // Give up, show raw
                        resultEl.innerHTML = `<div class="result-card"><div style="color:var(--yellow);margin-bottom:8px;font-size:12px">⚠️ LLM 返回格式异常，原始内容如下：</div><pre style="white-space:pre-wrap;color:var(--text-secondary);font-size:13px;">${escapeHtml(result)}</pre></div>`;
                        resultEl.classList.remove("hidden");
                        return;
                    }
                }
            } else {
                resultEl.innerHTML = `<div class="result-card"><pre style="white-space:pre-wrap;color:var(--text-secondary);font-size:13px;">${escapeHtml(result)}</pre></div>`;
                resultEl.classList.remove("hidden");
                return;
            }
        }

        // Save to history
        AddHistory(word, result).catch(console.error);

        // Render result
        resultEl.innerHTML = renderWordResult(parsed);
        resultEl.classList.remove("hidden");
        resultEl.scrollTop = 0;
    } catch (err: any) {
        loadingEl.classList.add("hidden");
        showError(String(err));
    } finally {
        isSearching = false;
    }
}

function renderWordResult(data: any): string {
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

    // Definitions
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

    // Memory tips
    if (data.memory_tips) {
        html += '<div class="section-title">🧠 记忆技巧</div>';
        html += `<div class="section-content">${escapeHtml(data.memory_tips)}</div>`;
    }

    // Synonyms
    if (data.synonyms) {
        html += '<div class="section-title">📌 近义词</div>';
        html += '<div class="section-content">';
        const syns = String(data.synonyms).split(/[,，、;；\s]+/).filter(Boolean);
        for (const s of syns) {
            html += `<span class="synonym-tag" data-word="${escapeHtml(s)}">${escapeHtml(s)}</span>`;
        }
        html += "</div>";
    }

    // Antonyms
    if (data.antonyms) {
        html += '<div class="section-title">🚫 反义词</div>';
        html += '<div class="section-content">';
        const ants = String(data.antonyms).split(/[,，、;；\s]+/).filter(Boolean);
        for (const a of ants) {
            html += `<span class="synonym-tag" data-word="${escapeHtml(a)}">${escapeHtml(a)}</span>`;
        }
        html += "</div>";
    }

    // Etymology
    if (data.etymology) {
        html += '<div class="section-title">📚 词源</div>';
        html += `<div class="section-content">${escapeHtml(data.etymology)}</div>`;
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
                        <span class="history-time">${escapeHtml(entry.createdAt)}</span>
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
        modalBody.innerHTML = renderWordResult(parsed);
        historyDetailModal.classList.remove("hidden");
    } catch (err) {
        console.error(err);
    }
}

async function deleteHistoryEntry(id: string) {
    try {
        await DeleteHistory(id);
        await showHistory(); // Refresh
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
    console.log("[WordWise] search click, value:", JSON.stringify(word));
    doSearch(word);
});
searchInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
        const word = searchInput.value;
        console.log("[WordWise] enter key, value:", JSON.stringify(word));
        doSearch(word);
    }
});

btnPaste.addEventListener("click", pasteAndSearch);

btnHistory.addEventListener("click", showHistory);
btnCloseHistory.addEventListener("click", () => historyPanel.classList.add("hidden"));

btnSettings.addEventListener("click", showSettings);
btnCloseSettings.addEventListener("click", () => settingsPanel.classList.add("hidden"));

btnSaveConfig.addEventListener("click", saveConfig);

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

    // Ignore single modifier keys
    const key = e.key;
    if (["Control", "Alt", "Shift", "Meta", "Super"].includes(key)) return;

    // Build accelerator string
    const parts: string[] = [];
    if (e.ctrlKey) parts.push("Ctrl");
    if (e.altKey) parts.push("Alt");
    if (e.shiftKey) parts.push("Shift");
    if (e.metaKey) parts.push("Super");

    // Map key names
    let keyName = key;
    if (key === " ") keyName = "Space";
    else if (key.length === 1) keyName = key.toUpperCase();
    else if (key.startsWith("F") && /^F\d+$/.test(key)) keyName = key;
    else if (key === "Escape") { stopRecording(); return; }
    else if (key === "Backspace") { stopRecording(); inputShortcutKey.value = ""; return; }
    else keyName = key;

    parts.push(keyName);
    const accelerator = parts.join("+");

    // Must have at least one modifier + one key
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
        // Don't hide if modal or panel is open
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
    // Hide window (minimize to tray)
    // In v3, we can emit an event to Go to hide the window
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
        // Click on the item itself -> view
        const item = target.closest(".history-item") as HTMLElement;
        if (item && item.dataset.id) {
            viewHistoryEntry(item.dataset.id);
        }
    }
});

// Click synonym tags to search
resultEl.addEventListener("click", (e) => {
    const target = e.target as HTMLElement;
    if (target.classList.contains("synonym-tag") && target.dataset.word) {
        doSearch(target.dataset.word);
    }
});
modalBody.addEventListener("click", (e) => {
    const target = e.target as HTMLElement;
    if (target.classList.contains("synonym-tag") && target.dataset.word) {
        historyDetailModal.classList.add("hidden");
        doSearch(target.dataset.word);
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
