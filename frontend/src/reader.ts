// ============================================================
// WordFlow — Reading feature (reader window)
// Three-part layout: material list | reading | interactive area.
// Design decisions: reading-feature-design.md
// ============================================================

import {
    CreateMaterial, UpdateMaterial, DeleteMaterial, GetMaterials, GetMaterial,
    SetMark, GetMarks, TranslateParagraph, GetTranslations, AutoTitle, AskQuestion,
    GetChatHistory, ClearChat,
} from "../bindings/wordflow/readingservice.js";
import {
    LookupWordFast, LookupWordLLMFast, LookupWordCached, CacheResult, LookupWordAudio,
} from "../bindings/wordflow/dictservice.js";
import { AddHistory } from "../bindings/wordflow/historyservice.js";
import type {
    ReadingMaterial, WordMark, ParagraphTranslation, ChatMsg,
} from "../bindings/wordflow/models.js";

// Mark status constants (mirror reading_service.go MARK_LOOKED_UP/MARK_SAVED).
const MARK_LOOKED_UP = 1;
const MARK_SAVED = 2;

// ------------------------------------------------------------
// DOM refs
// ------------------------------------------------------------
const $ = (id: string) => document.getElementById(id) as HTMLElement;
const materialListEl = $("material-list");
const materialListPanelEl = $("material-list-panel");
const btnCollapseList = $("btn-collapse-list") as HTMLButtonElement;
const btnExpandList = $("btn-expand-list") as HTMLButtonElement;
const interactivePanelEl = $("interactive-panel");
const interactiveResizerEl = $("interactive-resizer");
const readingContentEl = $("reading-content");
const readingToolbarEl = $("reading-toolbar");
const readingTitleEl = $("reading-title");
const btnBackList = $("btn-back-list") as HTMLButtonElement;
const btnFullTranslate = $("btn-full-translate") as HTMLButtonElement;
const btnFontPlus = $("btn-font-plus") as HTMLButtonElement;
const btnFontMinus = $("btn-font-minus") as HTMLButtonElement;
const fontSizeDisplay = $("font-size-display");
const btnAddMaterial = $("btn-add-material") as HTMLButtonElement;

const wordCardEl = $("word-card");
const wordCardWordEl = $("word-card-word");
const wordCardBodyEl = $("word-card-body");
const btnWordCardClose = $("word-card-close") as HTMLButtonElement;
const btnDeepDive = $("btn-deep-dive") as HTMLButtonElement;
const btnSaveWord = $("btn-save-word") as HTMLButtonElement;

const chatMessagesEl = $("chat-messages");
const chatInputEl = $("chat-input") as HTMLTextAreaElement;
const btnChatSend = $("btn-chat-send") as HTMLButtonElement;
const btnClearChat = $("btn-clear-chat") as HTMLButtonElement;

const formModalEl = $("material-form-modal");
const formTitleEl = $("material-form-title");
const titleInputEl = $("material-title") as HTMLInputElement;
const contentTextareaEl = $("material-content") as HTMLTextAreaElement;
const btnAutoTitle = $("btn-auto-title") as HTMLButtonElement;
const btnSaveMaterial = $("btn-save-material") as HTMLButtonElement;
const btnCancelMaterial = $("btn-cancel-material") as HTMLButtonElement;
const btnCloseMaterialForm = $("btn-close-material-form") as HTMLButtonElement;
const titleHintEl = $("material-title-hint");
const contentHintEl = $("material-content-hint");

const MAX_MATERIAL_CHARS = 20000;

// ------------------------------------------------------------
// State
// ------------------------------------------------------------
let materials: ReadingMaterial[] = [];
let currentMaterial: ReadingMaterial | null = null;
let marks = new Map<string, number>(); // lowercase word/phrase → status
let translations = new Map<number, string>(); // paragraph index → translation

let currentWord = "";
let currentEcdict: any = null;   // parsed ECDICT JSON of currentWord
let currentResult: any = null;   // merged JSON to save (ECDICT or ECDICT+LLM)
let deepDiveInFlight = false;
let savingWord = false;
let chatSending = false;
let editingMaterialId: string | null = null;
let pendingSelection = "";

// ------------------------------------------------------------
// Init
// ------------------------------------------------------------
async function init() {
    await refreshMaterials();
    bindEvents();
}

async function refreshMaterials(activeId?: string) {
    try {
        materials = (await GetMaterials()) || [];
    } catch (e) {
        materials = [];
        console.error("load materials failed", e);
    }
    renderMaterialList(activeId);
}

function bindEvents() {
    btnAddMaterial.addEventListener("click", () => openMaterialForm(null));
    btnCollapseList.addEventListener("click", () => setListCollapsed(true));
    btnExpandList.addEventListener("click", () => setListCollapsed(false));
    initPanelLayout();
    initPanelResizer();
    btnBackList.addEventListener("click", backToList);
    btnFullTranslate.addEventListener("click", fullTranslate);
    initFontSizeControls();
    btnWordCardClose.addEventListener("click", hideWordCard);
    btnDeepDive.addEventListener("click", deepDive);
    btnSaveWord.addEventListener("click", saveWord);
    btnChatSend.addEventListener("click", sendChat);
    btnClearChat.addEventListener("click", clearChat);
    btnSaveMaterial.addEventListener("click", saveMaterialFromForm);
    btnCancelMaterial.addEventListener("click", closeMaterialForm);
    btnCloseMaterialForm.addEventListener("click", closeMaterialForm);
    btnAutoTitle.addEventListener("click", autoTitleFromForm);

    // Reading area interactions (event delegation)
    readingContentEl.addEventListener("click", onReadingClick);
    document.addEventListener("mouseup", onReadingMouseUp);

    // Chat send: Ctrl+Enter in the textarea. Focusing the chat box hides the
    // word card so the chat gets the full panel height.
    chatInputEl.addEventListener("focus", hideWordCard);
    chatInputEl.addEventListener("keydown", (e) => {
        if (e.key === "Enter" && e.ctrlKey) {
            e.preventDefault();
            sendChat();
        }
    });

    // Global shortcuts: Alt+S save, Enter deep-dive.
    // Guard: never fire while typing in an input/textarea, and never steal
    // Enter from a focused button (it activates the button instead).
    document.addEventListener("keydown", (e) => {
        const t = e.target as HTMLElement | null;
        const tag = t ? t.tagName : "";
        const isEditable = tag === "INPUT" || tag === "TEXTAREA" || !!t?.isContentEditable;
        if (e.altKey && (e.key === "s" || e.key === "S")) {
            if (isEditable) return;
            e.preventDefault();
            saveWord();
        } else if (e.key === "Enter" && !isEditable) {
            if (tag === "BUTTON") return; // let the focused button handle Enter
            e.preventDefault();
            deepDive();
        }
    });
}

// ------------------------------------------------------------
// Panel layout: foldable left list + adjustable right panel width
// ------------------------------------------------------------
const RIGHT_WIDTH_KEY = "reader-right-width";
const LEFT_COLLAPSED_KEY = "reader-left-collapsed";
const RIGHT_WIDTH_MIN = 320;
const RIGHT_WIDTH_MAX = 640;

function initPanelLayout() {
    // Restore persisted right-panel width.
    const w = Number(localStorage.getItem(RIGHT_WIDTH_KEY));
    if (w && w >= RIGHT_WIDTH_MIN && w <= RIGHT_WIDTH_MAX) {
        interactivePanelEl.style.width = w + "px";
    }
    // Restore persisted collapsed state.
    setListCollapsed(localStorage.getItem(LEFT_COLLAPSED_KEY) === "1");
}

function setListCollapsed(collapsed: boolean) {
    materialListPanelEl.classList.toggle("collapsed", collapsed);
    localStorage.setItem(LEFT_COLLAPSED_KEY, collapsed ? "1" : "0");
}

function initPanelResizer() {
    let startX = 0;
    let startW = 0;
    interactiveResizerEl.addEventListener("mousedown", (e) => {
        startX = e.clientX;
        startW = interactivePanelEl.offsetWidth;
        interactiveResizerEl.classList.add("active");
        document.body.classList.add("resizing");
        document.addEventListener("mousemove", onMove);
        document.addEventListener("mouseup", onUp);
        e.preventDefault();
    });
    const onMove = (e: MouseEvent) => {
        // Divider follows the cursor: drag right → right panel narrower,
        // drag left → right panel wider.
        const w = Math.min(RIGHT_WIDTH_MAX, Math.max(RIGHT_WIDTH_MIN, startW - (e.clientX - startX)));
        interactivePanelEl.style.width = w + "px";
    };
    const onUp = () => {
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
        interactiveResizerEl.classList.remove("active");
        document.body.classList.remove("resizing");
        localStorage.setItem(RIGHT_WIDTH_KEY, String(interactivePanelEl.offsetWidth));
    };
}

// ------------------------------------------------------------
// Material list
// ------------------------------------------------------------
function renderMaterialList(activeId?: string) {
    materialListEl.innerHTML = "";
    if (materials.length === 0) {
        materialListEl.innerHTML =
            '<div class="empty-list">还没有学习材料。<br/>点击 ➕ 粘贴一篇英文文章开始阅读。</div>';
        return;
    }
    for (const m of materials) {
        const div = document.createElement("div");
        div.className = "material-item" + (m.id === activeId ? " active" : "");
        const wc = m.wordCount || countWords(m.content);
        const saved = m.savedCount || 0;
        div.innerHTML = `
            <div class="material-item-title">${escapeHtml(m.title)}</div>
            <div class="material-item-meta">
                <span>${wc} 词</span>
                <span>更新 ${formatRelativeDate(m.updatedAt || m.createdAt)}</span>
                ${saved ? `<span class="mark-saved">★ ${saved}</span>` : ""}
            </div>
            <div class="material-item-actions">
                <button class="secondary-btn small" data-act="edit">✎ 编辑</button>
                <button class="danger-btn small" data-act="delete">🗑 删除</button>
            </div>`;
        div.addEventListener("click", async (e) => {
            const act = (e.target as HTMLElement).dataset.act;
            if (act === "edit") {
                e.stopPropagation();
                // List items don't carry content — fetch the full material
                // so the edit form is pre-filled (title + body).
                try {
                    const full = await GetMaterial(m.id);
                    if (full) openMaterialForm(full);
                } catch (err) {
                    toast(`加载材料失败: ${err}`);
                }
            } else if (act === "delete") {
                e.stopPropagation();
                confirmDeleteMaterial(m);
            } else {
                openMaterial(m.id);
            }
        });
        materialListEl.appendChild(div);
    }
}

async function confirmDeleteMaterial(m: ReadingMaterial) {
    if (!confirm(`删除材料「${m.title}」？\n其生词标记、翻译和聊天记录也会一并删除。`)) return;
    try {
        await DeleteMaterial(m.id);
        if (currentMaterial && currentMaterial.id === m.id) backToList();
        await refreshMaterials();
    } catch (e) {
        toast(`删除失败: ${e}`);
    }
}

// ------------------------------------------------------------
// Material form (add / edit)
// ------------------------------------------------------------
function openMaterialForm(material?: ReadingMaterial) {
    editingMaterialId = material?.id ?? null;
    formTitleEl.textContent = material ? "编辑材料" : "添加材料";
    titleInputEl.value = material?.title ?? "";
    contentTextareaEl.value = material?.content ?? "";
    titleHintEl.textContent = "";
    contentHintEl.textContent = "";
    btnAutoTitle.disabled = false;
    btnAutoTitle.textContent = "✨ 自动生成";
    formModalEl.classList.remove("hidden");
    titleInputEl.focus();
}

function closeMaterialForm() {
    formModalEl.classList.add("hidden");
    editingMaterialId = null;
}

async function saveMaterialFromForm() {
    const title = titleInputEl.value.trim();
    const content = contentTextareaEl.value;
    if (!content.trim()) {
        contentHintEl.textContent = "内容不能为空";
        contentHintEl.classList.add("field-error");
        return;
    }
    if (content.length > MAX_MATERIAL_CHARS) {
        contentHintEl.textContent = `内容过长：最多 ${MAX_MATERIAL_CHARS} 字符（当前 ${content.length}）`;
        contentHintEl.classList.add("field-error");
        return;
    }
    contentHintEl.textContent = "";
    btnSaveMaterial.disabled = true;
    try {
        if (editingMaterialId) {
            await UpdateMaterial(editingMaterialId, title, content);
            closeMaterialForm();
            await refreshMaterials();
            // Reload the reading view if this material was open (fresh marks/translations).
            if (currentMaterial && currentMaterial.id === editingMaterialId) {
                await openMaterial(editingMaterialId);
            }
        } else {
            const id = await CreateMaterial(title, content);
            closeMaterialForm();
            await refreshMaterials();
            await openMaterial(id);
        }
    } catch (e) {
        contentHintEl.textContent = String(e);
        contentHintEl.classList.add("field-error");
    } finally {
        btnSaveMaterial.disabled = false;
    }
}

async function autoTitleFromForm() {
    const content = contentTextareaEl.value;
    if (!content.trim()) {
        titleHintEl.textContent = "请先粘贴内容，再自动生成标题";
        titleHintEl.classList.add("field-error");
        return;
    }
    btnAutoTitle.disabled = true;
    btnAutoTitle.textContent = "生成中...";
    try {
        const title = await AutoTitle(content);
        titleInputEl.value = title;
        titleHintEl.textContent = "";
    } catch (e) {
        titleHintEl.textContent = `生成失败: ${e}（请检查 LLM 配置）`;
        titleHintEl.classList.add("field-error");
    } finally {
        btnAutoTitle.disabled = false;
        btnAutoTitle.textContent = "✨ 自动生成";
    }
}

// ------------------------------------------------------------

// ------------------------------------------------------------
// Reading font size (A− / A+), persisted
// ------------------------------------------------------------
const FONT_SIZE_KEY = "reader-font-size";
const FONT_MIN = 16;
const FONT_MAX = 24;
let readingFontSize = 18;

function initFontSizeControls() {
    const saved = Number(localStorage.getItem(FONT_SIZE_KEY));
    if (saved && saved >= FONT_MIN && saved <= FONT_MAX) readingFontSize = saved;
    applyReadingFontSize();
    btnFontPlus.addEventListener("click", () => {
        readingFontSize = Math.min(FONT_MAX, readingFontSize + 1);
        applyReadingFontSize();
    });
    btnFontMinus.addEventListener("click", () => {
        readingFontSize = Math.max(FONT_MIN, readingFontSize - 1);
        applyReadingFontSize();
    });
}

function applyReadingFontSize() {
    readingContentEl.style.setProperty("--reading-font-size", readingFontSize + "px");
    fontSizeDisplay.textContent = String(readingFontSize);
    localStorage.setItem(FONT_SIZE_KEY, String(readingFontSize));
}
// Open material → reading view
// ------------------------------------------------------------
async function openMaterial(id: string) {
    let m: ReadingMaterial | null = null;
    try {
        m = await GetMaterial(id);
    } catch (e) {
        toast(`加载材料失败: ${e}`);
        return;
    }
    if (!m) {
        toast("材料不存在");
        return;
    }
    currentMaterial = m;
    const [mk, tr, ch] = await Promise.all([
        GetMarks(id).catch(() => null),
        GetTranslations(id).catch(() => null),
        GetChatHistory(id).catch(() => null),
    ]);
    marks = new Map((mk || []).map((w: WordMark) => [w.word, w.status]));
    translations = new Map((tr || []).map((t: ParagraphTranslation) => [t.paragraphIndex, t.translation]));
    renderReadingView();
    renderChat((ch || []) as ChatMsg[]);
    renderMaterialList(id);
    hideWordCard();
}

function backToList() {
    currentMaterial = null;
    marks.clear();
    translations.clear();
    readingContentEl.innerHTML = "";
    readingToolbarEl.classList.add("hidden");
    renderMaterialList();
    renderChat([]);
    hideWordCard();
}

function renderReadingView() {
    if (!currentMaterial) return;
    readingToolbarEl.classList.remove("hidden");
    readingTitleEl.textContent = currentMaterial.title;
    btnFullTranslate.disabled = false;
    btnFullTranslate.textContent = "🌐 全文翻译";
    renderParagraphs();
}

// splitParagraphs mirrors reading_service.go splitParagraphs exactly.
function splitParagraphs(content: string): string[] {
    content = content.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
    const pars: string[] = [];
    let cur: string[] = [];
    const flush = () => {
        const t = cur.map(s => s.trim()).join(" ").trim();
        if (t) pars.push(t);
        cur = [];
    };
    for (const line of content.split("\n")) {
        if (line.trim() === "") { flush(); continue; }
        cur.push(line.trim());
    }
    flush();
    return pars;
}

interface Token { isWord: boolean; text: string; }

function tokenize(text: string): Token[] {
    const tokens: Token[] = [];
    const re = /[A-Za-z]+(?:['’][A-Za-z]+)*/g;
    let last = 0;
    let m: RegExpExecArray | null;
    while ((m = re.exec(text)) !== null) {
        if (m.index > last) tokens.push({ isWord: false, text: text.slice(last, m.index) });
        tokens.push({ isWord: true, text: m[0] });
        last = m.index + m[0].length;
    }
    if (last < text.length) tokens.push({ isWord: false, text: text.slice(last) });
    return tokens;
}

function renderParagraphs() {
    if (!currentMaterial) return;
    const scrollTop = readingContentEl.scrollTop;
    const paras = splitParagraphs(currentMaterial.content);
    if (paras.length === 0) {
        readingContentEl.innerHTML = '<div class="reading-empty">这个材料没有内容。</div>';
        return;
    }
    let html = "";
    for (let i = 0; i < paras.length; i++) html += renderParagraph(paras[i], i);
    readingContentEl.innerHTML = html;
    readingContentEl.scrollTop = scrollTop;
}


function renderParagraph(text: string, index: number): string {
    const tokens = tokenize(text);
    let inner = "";
    for (const t of tokens) {
        if (t.isWord) {
            const w = t.text.toLowerCase();
            const status = marks.get(w);
            const cls = status === MARK_SAVED
                ? "reading-word mark-saved"
                : status === MARK_LOOKED_UP
                    ? "reading-word mark-looked-up"
                    : "reading-word";
            inner += `<span class="${cls}" data-word="${escapeHtml(w)}">${escapeHtml(t.text)}</span>`;
        } else {
            inner += escapeHtml(t.text);
        }
    }
    const tr = translations.get(index);
    const trHtml = tr
        ? `<div class="para-translation" data-pidx="${index}" title="点击隐藏翻译">${escapeHtml(tr)}</div>`
        : "";
    // Show the translate button ONLY for untranslated paragraphs — a
    // translated paragraph already shows its translation block below.
    const btnHtml = tr
        ? ""
        : `<button class="para-translate-btn secondary-btn small" data-pidx="${index}" title="翻译本段">译</button>`;
    return `<div class="reading-paragraph" data-pidx="${index}">
        ${btnHtml}${inner}${trHtml}</div>`;
}

// ------------------------------------------------------------
// Reading interactions: click word / drag-select / paragraph translate
// ------------------------------------------------------------
function onReadingMouseUp() {
    const sel = window.getSelection();
    if (sel && !sel.isCollapsed && sel.toString().trim()) {
        pendingSelection = sel.toString().trim();
        // Safety net: drop the selection if the following click never comes
        // (e.g. the user clicked outside the reading area).
        window.setTimeout(() => { pendingSelection = ""; }, 800);
    }
}

function clearPendingSelection() {
    pendingSelection = "";
}

function onReadingClick(e: MouseEvent) {
    const target = e.target as HTMLElement;

    // Paragraph translate button
    if (target.classList.contains("para-translate-btn")) {
        const pidx = Number(target.dataset.pidx);
        clearPendingSelection();
        translateParagraph(pidx);
        return;
    }

    // Click an existing translation to hide it (it can be re-translated later)
    if (target.classList.contains("para-translation")) {
        const pidx = Number(target.dataset.pidx);
        clearPendingSelection();
        translations.delete(pidx);
        renderParagraphs();
        return;
    }

    // Word click (or drag-selected phrase — click fires after mouseup)
    if (target.classList.contains("reading-word")) {
        if (pendingSelection) {
            const phrase = pendingSelection;
            clearPendingSelection();
            if (phrase) lookupWord(phrase);
            return;
        }
        const w = target.dataset.word || "";
        if (w) lookupWord(w);
        return;
    }

    // Click elsewhere — drop any stale selection
    clearPendingSelection();
}

async function translateParagraph(pidx: number) {
    if (!currentMaterial) return;
    const paras = splitParagraphs(currentMaterial.content);
    if (pidx < 0 || pidx >= paras.length) return;
    if (translations.has(pidx)) {
        translations.delete(pidx); // toggle off
        renderParagraphs();
        return;
    }
    const setBtnText = (text: string, disabled?: boolean) => {
        const b = document.querySelector(`.para-translate-btn[data-pidx="${pidx}"]`) as HTMLButtonElement;
        if (b) {
            b.textContent = text;
            if (disabled !== undefined) b.disabled = disabled;
        }
    };
    setBtnText("翻译中...", true);
    try {
        const t = await TranslateParagraph(currentMaterial.id, pidx, paras[pidx]);
        if (t) translations.set(pidx, t);
        renderParagraphs();
        // Flash the "✓ 已译" indicator briefly, then revert to the short
        // "译" label so it never blocks the paragraph content.
        setBtnText("✓ 已译", false);
        setTimeout(() => setBtnText("译"), 1500);
    } catch (err) {
        toast(`翻译失败: ${err}`);
        setBtnText("译", false);
    }
}

async function fullTranslate() {
    if (!currentMaterial) return;
    const paras = splitParagraphs(currentMaterial.content);
    if (paras.length === 0) return;
    btnFullTranslate.disabled = true;
    for (let i = 0; i < paras.length; i++) {
        btnFullTranslate.textContent = `翻译中 ${i + 1}/${paras.length}`;
        if (translations.has(i)) continue;
        try {
            const t = await TranslateParagraph(currentMaterial.id, i, paras[i]);
            if (t) translations.set(i, t);
        } catch (err) {
            toast(`翻译第 ${i + 1} 段失败: ${err}`);
            break;
        }
    }
    btnFullTranslate.textContent = "🌐 全文翻译";
    btnFullTranslate.disabled = false;
    renderParagraphs();
}

// ------------------------------------------------------------
// Word lookup (instant ECDICT → on-demand LLM deep-dive)
// ------------------------------------------------------------
async function lookupWord(word: string) {
    if (!currentMaterial) return;
    currentWord = word;
    currentEcdict = null;
    currentResult = null;
    openWordCard();
    wordCardWordEl.textContent = word;
    wordCardBodyEl.innerHTML = '<div class="word-card-loading">查询中...</div>';

    // Record looked-up mark (requirement #9).
    // SetMark only ever escalates (saved=2 wins over looked-up=1); mirror
    // that rule here so a previously-saved word is never shown as un-saved.
    try {
        await SetMark(currentMaterial.id, word, MARK_LOOKED_UP);
        const key = word.toLowerCase();
        marks.set(key, Math.max(marks.get(key) || 0, MARK_LOOKED_UP));
        renderParagraphs();
    } catch (e) { /* non-critical */ }

    // 1) Full cached result (in-memory LRU or history DB) — a word that was
    // already AI-deep-dived (or looked up before) shows the full card again.
    const cached = await LookupWordCached(word);
    if (cached) {
        try {
            const data = JSON.parse(cached);
            if (data) {
                currentResult = data;
                renderWordCardFull(data);
                return;
            }
        } catch (e) { /* fall through to ECDICT */ }
    }

    // 2) Instant ECDICT (~0.1ms)
    const json = await LookupWordFast(word);
    if (!json) {
        wordCardBodyEl.innerHTML =
            '<div class="word-card-error">离线词典未找到该词。试试 ✨ AI 深入（Enter）。</div>';
        updateSaveButtonState();
        return;
    }
    let data: any;
    try {
        data = JSON.parse(json);
    } catch (e) {
        data = { word };
    }
    currentEcdict = data;
    currentResult = data;
    renderWordCardCompact(data);
}

function renderWordCardCompact(data: any) {
    const speakBtn = `<button class="icon-btn speak-btn" data-word="${escapeHtml(currentWord)}" title="发音">🔊</button>`;
    let html = `<div class="word-card-phonetic">${speakBtn}`;
    if (data.phonetic) html += ` ${escapeHtml(data.phonetic)}`;
    html += `</div>`;
    if (data.translation) {
        html += `<div class="word-card-translation">${escapeHtml(data.translation)}</div>`;
    }
    if (data.corrected_from) {
        html += `<div class="word-card-extra">💡 您是不是想找：<strong>${escapeHtml(data.word || currentWord)}</strong>？（原始输入：${escapeHtml(data.corrected_from)}）</div>`;
    }
    wordCardBodyEl.innerHTML = html;
    bindSpeakButtons();
    updateSaveButtonState();
}

function renderWordCardFull(data: any) {
    const speakBtn = `<button class="icon-btn speak-btn" data-word="${escapeHtml(currentWord)}" title="发音">🔊</button>`;
    let html = `<div class="word-card-phonetic">${speakBtn}`;
    if (data.phonetic) html += ` ${escapeHtml(data.phonetic)}`;
    html += `</div>`;
    if (data.translation) html += `<div class="word-card-translation">${escapeHtml(data.translation)}</div>`;

    const defs: any[] = data.definitions || [];
    if (defs.length) {
        html += '<div class="word-card-defs">';
        for (const d of defs) {
            html += '<div class="def-item">';
            if (d.pos) html += `<span class="def-pos">${escapeHtml(d.pos)}</span>`;
            if (d.meaning) html += `<span>${escapeHtml(d.meaning)}</span>`;
            if (d.example) html += `<div class="word-card-extra">📝 ${escapeHtml(d.example)}</div>`;
            if (d.chineseExample) html += `<div class="word-card-extra">💡 ${escapeHtml(d.chineseExample)}</div>`;
            html += '</div>';
        }
        html += '</div>';
    }

    const extraFields: [string, string][] = [];
    for (const k of ["memory_tips", "synonyms", "antonyms", "etymology", "pronunciation"]) {
        if (data[k]) {
            const labels: Record<string, string> = {
                memory_tips: "🧠 记忆技巧",
                synonyms: "📌 近义词",
                antonyms: "🚫 反义词",
                etymology: "📚 词源",
                pronunciation: "🗣️ 发音说明",
            };
            extraFields.push([labels[k] || k, String(data[k])]);
        }
    }
    for (const [label, val] of extraFields) {
        html += `<div class="word-card-extra"><b>${label}</b><br/>${escapeHtml(val)}</div>`;
    }

    const src = Array.isArray(data._sources) ? data._sources.join(" + ") : "";
    if (src) html += `<div class="word-card-extra" style="color:var(--text-muted);font-size:11px;">数据来源: ${escapeHtml(src)}</div>`;
    wordCardBodyEl.innerHTML = html;
    bindSpeakButtons();
    updateSaveButtonState();
}

async function deepDive() {
    if (!currentWord || !currentMaterial || deepDiveInFlight) return;
    if (currentResult && Array.isArray(currentResult._sources) && currentResult._sources.includes("LLM")) return;
    deepDiveInFlight = true;
    btnDeepDive.disabled = true;
    btnDeepDive.textContent = "AI 深入中...";
    try {
        const raw = await LookupWordLLMFast(currentWord);
        if (!raw) throw new Error("AI 未返回内容");
        const match = raw.match(/\{[\s\S]*\}/);
        if (!match) throw new Error("AI 返回格式异常");
        const llm = JSON.parse(match[0]);
        const merged = mergeForReader(currentEcdict, llm);
        currentResult = merged;
        CacheResult(currentWord, JSON.stringify(merged)).catch(() => {});
        renderWordCardFull(merged);
    } catch (err) {
        wordCardBodyEl.innerHTML += `<div class="word-card-error">AI 深入失败: ${escapeHtml(String(err))}</div>`;
    } finally {
        deepDiveInFlight = false;
        btnDeepDive.disabled = false;
        btnDeepDive.textContent = "✨ AI 深入";
    }
}

function mergeForReader(ecdict: any, llm: any): any {
    const merged: any = { word: llm?.word || ecdict?.word || currentWord };
    merged.phonetic = ecdict?.phonetic || llm?.phonetic || "";
    merged.translation = ecdict?.translation || llm?.translation || "";
    merged.definitions = (llm?.definitions && Array.isArray(llm.definitions) && llm.definitions.length)
        ? llm.definitions
        : parseEcdictDefinitions(ecdict?.definition, ecdict?.pos);
    for (const k of Object.keys(llm || {})) {
        if (["word", "phonetic", "translation", "definitions"].includes(k)) continue;
        merged[k] = llm[k];
    }
    merged.collins = ecdict?.collins ?? null;
    merged.oxford = ecdict?.oxford ?? null;
    merged.tag = ecdict?.tag || "";
    merged.bnc = ecdict?.bnc ?? null;
    merged.frq = ecdict?.frq ?? null;
    merged.exchange = ecdict?.exchange || "";
    merged._sources = [];
    if (ecdict) merged._sources.push("ECDICT");
    if (llm) merged._sources.push("LLM");
    return merged;
}

function parseEcdictDefinitions(definition: string, pos?: string): any[] {
    if (!definition) return [];
    const lines = definition.split("\n").filter(Boolean);
    if (lines.length === 0) return [];
    const groups: any[] = [];
    let currentGroup: any = null;
    for (const line of lines) {
        const posMatch = line.match(/^([a-z]+\.)\s*/);
        if (posMatch) {
            currentGroup = { pos: posMatch[1], meaning: line.replace(posMatch[0], "").trim() };
            groups.push(currentGroup);
        } else if (currentGroup) {
            currentGroup.meaning += "; " + line.trim();
        } else {
            currentGroup = { pos: pos || "", meaning: line.trim() };
            groups.push(currentGroup);
        }
    }
    return groups;
}

// ------------------------------------------------------------
// Word card / save (requirement #8)
// ------------------------------------------------------------
function openWordCard() {
    wordCardEl.classList.remove("hidden");
    btnDeepDive.textContent = "✨ AI 深入";
    btnDeepDive.disabled = false;
    updateSaveButtonState();
}

function hideWordCard() {
    wordCardEl.classList.add("hidden");
    currentWord = "";
    currentEcdict = null;
    currentResult = null;
}

function updateSaveButtonState() {
    if (!currentWord) return;
    const status = marks.get(currentWord.toLowerCase());
    if (status === MARK_SAVED) {
        btnSaveWord.textContent = "✓ 已保存";
        btnSaveWord.disabled = true;
    } else {
        btnSaveWord.textContent = "💾 保存到生词本";
        btnSaveWord.disabled = savingWord;
    }
    // Already AI-deep-dived (or a full cached result)? Disable deep-dive.
    const hasLLM = !!currentResult && Array.isArray(currentResult._sources) && currentResult._sources.includes("LLM");
    if (hasLLM) {
        btnDeepDive.textContent = "✨ 已深入";
        btnDeepDive.disabled = true;
    } else {
        btnDeepDive.textContent = "✨ AI 深入";
        btnDeepDive.disabled = deepDiveInFlight;
    }
}

async function saveWord() {
    if (!currentWord || !currentMaterial || savingWord) return;
    if (!currentResult) {
        toast("请先查询该词再保存（点击文章中的单词）");
        return;
    }
    savingWord = true;
    btnSaveWord.disabled = true;
    try {
        // AddHistory upserts by word and auto-pushes to the SyncServer.
        await AddHistory(currentWord, JSON.stringify(currentResult));
        await SetMark(currentMaterial.id, currentWord, MARK_SAVED);
        const key = currentWord.toLowerCase();
        marks.set(key, Math.max(marks.get(key) || 0, MARK_SAVED));
        renderParagraphs();
        btnSaveWord.textContent = "✓ 已保存";
        // Refresh the list so its ★ saved-count stays accurate.
        refreshMaterials(currentMaterial.id);
        toast(`已保存「${currentWord}」并同步`);
    } catch (err) {
        btnSaveWord.textContent = "💾 保存到生词本";
        btnSaveWord.disabled = false;
        toast(`保存失败: ${err}`);
    } finally {
        savingWord = false;
    }
}

// ------------------------------------------------------------
// Speak
// ------------------------------------------------------------
function bindSpeakButtons() {
    const btns = wordCardBodyEl.querySelectorAll(".speak-btn");
    btns.forEach(b => {
        b.addEventListener("click", async (e) => {
            e.stopPropagation();
            const word = (b as HTMLElement).dataset.word || currentWord;
            speakTTS(word); // immediate feedback
            try {
                const audioUrl = await LookupWordAudio(word);
                if (audioUrl) speakWord(word, audioUrl);
            } catch { /* TTS already playing */ }
        });
    });
}

function speakWord(word: string, audioUrl?: string | null) {
    if (audioUrl) {
        const audio = new Audio(audioUrl);
        audio.play().catch(() => speakTTS(word));
        return;
    }
    speakTTS(word);
}

function speakTTS(word: string) {
    if (!window.speechSynthesis) return;
    const synth = window.speechSynthesis;
    synth.cancel();
    const utter = new SpeechSynthesisUtterance(word);
    utter.lang = 'en-US';
    utter.rate = 0.95;
    const voices = synth.getVoices();
    const voice =
        voices.find(v => v.lang === 'en-US') ??
        voices.find(v => v.lang?.toLowerCase().startsWith('en'));
    if (voice) utter.voice = voice;
    synth.speak(utter);
}

// ------------------------------------------------------------
// Chat (requirement #10)
// ------------------------------------------------------------
function renderChat(messages: ChatMsg[]) {
    chatMessagesEl.innerHTML = "";
    if (messages.length === 0) {
        chatMessagesEl.innerHTML =
            '<div class="chat-empty">就当前材料提问，AI 会结合材料内容回答。<br/>例如：「explain the metaphor in paragraph 3」</div>';
        return;
    }
    for (const m of messages) appendChatEl(m.role, m.content);
    chatMessagesEl.scrollTop = chatMessagesEl.scrollHeight;
}

function appendChat(role: string, content: string): HTMLElement {
    const el = appendChatEl(role, content);
    chatMessagesEl.scrollTop = chatMessagesEl.scrollHeight;
    return el;
}

function appendChatEl(role: string, content: string): HTMLElement {
    const div = document.createElement("div");
    div.className = "chat-bubble " + (role === "user" ? "user" : "assistant");
    div.textContent = content;
    chatMessagesEl.appendChild(div);
    return div;
}

async function sendChat() {
    const q = chatInputEl.value.trim();
    if (!q || !currentMaterial || chatSending) return;
    chatSending = true;
    chatInputEl.value = "";
    appendChat("user", q);
    const thinking = appendChat("assistant", "思考中...");
    try {
        const answer = await AskQuestion(currentMaterial.id, q);
        thinking.textContent = answer;
        chatMessagesEl.scrollTop = chatMessagesEl.scrollHeight;
    } catch (err) {
        thinking.classList.add("error");
        thinking.textContent = String(err);
    } finally {
        chatSending = false;
        chatInputEl.focus();
    }
}

async function clearChat() {
    if (!currentMaterial) return;
    if (!confirm("清空与当前材料的聊天记录？")) return;
    try {
        await ClearChat(currentMaterial.id);
        renderChat([]);
    } catch (e) {
        toast(`清空失败: ${e}`);
    }
}

// ------------------------------------------------------------
// Utils
// ------------------------------------------------------------
function escapeHtml(s: string): string {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

function countWords(content: string): number {
    const m = content.match(/[A-Za-z]+(?:['’][A-Za-z]+)*/g);
    return m ? m.length : 0;
}

function formatRelativeDate(ts: number): string {
    if (!ts) return "";
    const diff = Math.max(0, Date.now() - ts * 1000);
    const minutes = Math.floor(diff / 60000);
    if (minutes < 1) return "刚刚";
    if (minutes < 60) return `${minutes} 分钟前`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `${hours} 小时前`;
    const days = Math.floor(hours / 24);
    if (days < 7) return `${days} 天前`;
    return new Date(ts * 1000).toLocaleDateString();
}

let toastTimer: number | undefined;
function toast(msg: string) {
    let el = document.getElementById("reader-toast");
    if (!el) {
        el = document.createElement("div");
        el.id = "reader-toast";
        el.className = "reader-toast";
        document.body.appendChild(el);
    }
    el.textContent = msg;
    el.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = window.setTimeout(() => el.classList.remove("show"), 3000);
}

// ------------------------------------------------------------
void init();
