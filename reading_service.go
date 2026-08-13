package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ============================================================
// ReadingService — English reading feature (PC client).
//
// Local-only SQLite storage for learning materials + word marks +
// paragraph translations + chat, plus LLM glue that reuses the
// DictService config (one shared LLM config per the design doc
// reading-feature-design.md).
// ============================================================

const (
	// MarkLookedUp = word was clicked & looked up (yellow underline in the text).
	MarkLookedUp = 1
	// MarkSaved = word was saved to the word book (green ★). Implies looked-up.
	MarkSaved = 2
)

const (
	maxMaterialChars  = 20000  // paste cap (see design doc Q3/Q6)
	chatTokenBudget   = 12000  // max estimated input tokens per chat call (design doc Q9)
	chatCharsPerToken = 3      // conservative chars/token estimate (mixed zh/en)
)

type ReadingMaterial struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	SavedCount int    `json:"savedCount"` // number of saved marks (status=2), for the list
	WordCount  int    `json:"wordCount"`  // English word count (for the list without shipping content)
}

type WordMark struct {
	Word   string `json:"word"`
	Status int    `json:"status"` // MarkLookedUp or MarkSaved
}

type ParagraphTranslation struct {
	ParagraphIndex int    `json:"paragraphIndex"`
	Translation    string `json:"translation"`
}

type ChatMsg struct {
	Role      string `json:"role"` // "user" | "assistant"
	Content   string `json:"content"`
	CreatedAt int64  `json:"createdAt"`
}

type ReadingService struct {
	app  *application.App
	dict *DictService // shared LLM config + client
	db   *sql.DB
	mu   sync.Mutex // serializes DB read-modify-write sequences
	once sync.Once
}

func getReadingDBPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	dir = filepath.Join(dir, "WordWise")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "reading.db")
}

func (r *ReadingService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	r.app = application.Get()
	r.once.Do(func() {
		if err := r.openDB(); err != nil {
			log.Printf("ReadingService: failed to open DB: %v", err)
		}
	})
	return nil
}

func (r *ReadingService) openDB() error {
	return r.openDBAt(getReadingDBPath())
}

func (r *ReadingService) openDBAt(dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return fmt.Errorf("创建阅读数据目录失败: %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("打开阅读数据库失败: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		log.Printf("Warning: set WAL mode failed: %v", err)
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		log.Printf("Warning: set synchronous failed: %v", err)
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS materials (
			id         TEXT PRIMARY KEY,
			title      TEXT NOT NULL DEFAULT '',
			content    TEXT NOT NULL DEFAULT '',
			word_count INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS material_words (
			material_id TEXT NOT NULL,
			word        TEXT NOT NULL,
			status      INTEGER NOT NULL DEFAULT 1,
			updated_at  INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (material_id, word)
		)`,
		`CREATE TABLE IF NOT EXISTS translations (
			material_id     TEXT NOT NULL,
			paragraph_index INTEGER NOT NULL,
			translation     TEXT NOT NULL DEFAULT '',
			created_at      INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (material_id, paragraph_index)
		)`,
		`CREATE TABLE IF NOT EXISTS chat_messages (
			id          TEXT PRIMARY KEY,
			material_id TEXT NOT NULL,
			role        TEXT NOT NULL,
			content     TEXT NOT NULL,
			created_at  INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_words_material ON material_words(material_id)`,
		`CREATE INDEX IF NOT EXISTS idx_chat_material ON chat_messages(material_id, created_at)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("创建阅读表失败: %v", err)
		}
	}

	// Migration: add word_count for DBs created before this column existed.
	var wcCol int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('materials') WHERE name = 'word_count'").Scan(&wcCol); err == nil && wcCol == 0 {
		if _, err := db.Exec("ALTER TABLE materials ADD COLUMN word_count INTEGER NOT NULL DEFAULT 0"); err != nil {
			log.Printf("Warning: add word_count column failed: %v", err)
		}
	}

	r.db = db
	log.Println("ReadingService started")
	return nil
}

func (r *ReadingService) ensureDB() error {
	if r.db == nil {
		return r.openDB()
	}
	return nil
}

// ------------------------------------------------------------
// Window
// ------------------------------------------------------------

// OpenReader shows the reading window, creating it on first use (singleton).
// The window really closes on ✕ (unlike the main popup, which hides to tray);
// reopening creates a fresh window and the UI restores state from SQLite.
func (r *ReadingService) OpenReader() error {
	app := application.Get()
	if w, ok := app.Window.Get("reader-window"); ok {
		w.Show()
		w.Focus()
		return nil
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "reader-window",
		Title:            "WordFlow - 英文阅读",
		Width:            1280,
		Height:           800,
		MinWidth:         1000,
		MinHeight:        640,
		BackgroundColour: application.NewRGB(30, 30, 46),
		URL:              "/reader.html",
	})
	log.Println("ReadingService: reader window created")
	return nil
}

// ------------------------------------------------------------
// Materials CRUD
// ------------------------------------------------------------

func (r *ReadingService) CreateMaterial(title, content string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureDB(); err != nil {
		return "", err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("内容不能为空")
	}
	if len([]rune(content)) > maxMaterialChars {
		return "", fmt.Errorf("内容过长：最多 %d 字符", maxMaterialChars)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "未命名材料"
	}
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now().Unix()
	wc := countEnglishWords(content)
	if _, err := r.db.Exec(
		"INSERT INTO materials (id, title, content, word_count, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, title, content, wc, now, now,
	); err != nil {
		return "", err
	}
	return id, nil
}

func (r *ReadingService) UpdateMaterial(id, title, content string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureDB(); err != nil {
		return err
	}
	var oldContent string
	err := r.db.QueryRow("SELECT content FROM materials WHERE id = ?", id).Scan(&oldContent)
	if err == sql.ErrNoRows {
		return fmt.Errorf("材料不存在")
	}
	if err != nil {
		return err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("内容不能为空")
	}
	if len([]rune(content)) > maxMaterialChars {
		return fmt.Errorf("内容过长：最多 %d 字符", maxMaterialChars)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "未命名材料"
	}
	now := time.Now().Unix()
	wc := countEnglishWords(content)
	if _, err := r.db.Exec(
		"UPDATE materials SET title = ?, content = ?, word_count = ?, updated_at = ? WHERE id = ?",
		title, content, wc, now, id,
	); err != nil {
		return err
	}

	// Prune translations whose paragraph changed or was removed.
	oldPars := splitParagraphs(oldContent)
	newPars := splitParagraphs(content)
	for i := 0; i < len(oldPars); i++ {
		if i >= len(newPars) || newPars[i] != oldPars[i] {
			if _, err := r.db.Exec(
				"DELETE FROM translations WHERE material_id = ? AND paragraph_index = ?",
				id, i,
			); err != nil {
				return err
			}
		}
	}

	// Prune orphan marks (words no longer present in the content).
	words := extractWordSet(content)
	rows, err := r.db.Query("SELECT word FROM material_words WHERE material_id = ?", id)
	if err != nil {
		return err
	}
	var orphans []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			rows.Close()
			return err
		}
		if !words[w] {
			orphans = append(orphans, w)
		}
	}
	rows.Close()
	for _, w := range orphans {
		if _, err := r.db.Exec(
			"DELETE FROM material_words WHERE material_id = ? AND word = ?", id, w,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReadingService) DeleteMaterial(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureDB(); err != nil {
		return err
	}
	for _, s := range []string{
		"DELETE FROM materials WHERE id = ?",
		"DELETE FROM material_words WHERE material_id = ?",
		"DELETE FROM translations WHERE material_id = ?",
		"DELETE FROM chat_messages WHERE material_id = ?",
	} {
		if _, err := r.db.Exec(s, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReadingService) GetMaterials() ([]ReadingMaterial, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(`
		SELECT m.id, m.title, m.word_count, m.created_at, m.updated_at,
		       (SELECT COUNT(*) FROM material_words w
		        WHERE w.material_id = m.id AND w.status = 2) AS saved_count
		FROM materials m
		ORDER BY m.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadingMaterial
	for rows.Next() {
		var m ReadingMaterial
		if err := rows.Scan(&m.ID, &m.Title, &m.WordCount, &m.CreatedAt, &m.UpdatedAt, &m.SavedCount); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *ReadingService) GetMaterial(id string) (ReadingMaterial, error) {
	if err := r.ensureDB(); err != nil {
		return ReadingMaterial{}, err
	}
	var m ReadingMaterial
	err := r.db.QueryRow(
		"SELECT id, title, content, word_count, created_at, updated_at FROM materials WHERE id = ?", id,
	).Scan(&m.ID, &m.Title, &m.Content, &m.WordCount, &m.CreatedAt, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return ReadingMaterial{}, fmt.Errorf("材料不存在")
	}
	return m, err
}

// ------------------------------------------------------------
// Word marks (looked-up / saved)
// ------------------------------------------------------------

func (r *ReadingService) SetMark(materialID, word string, status int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureDB(); err != nil {
		return err
	}
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" {
		return nil
	}
	if status != MarkLookedUp && status != MarkSaved {
		return fmt.Errorf("无效的标记状态: %d", status)
	}
	now := time.Now().Unix()
	var existing int
	err := r.db.QueryRow(
		"SELECT status FROM material_words WHERE material_id = ? AND word = ?", materialID, word,
	).Scan(&existing)
	if err == sql.ErrNoRows {
		_, err = r.db.Exec(
			"INSERT INTO material_words (material_id, word, status, updated_at) VALUES (?, ?, ?, ?)",
			materialID, word, status, now,
		)
		return err
	}
	if err != nil {
		return err
	}
	if status > existing { // saved (2) always wins over looked-up (1)
		_, err = r.db.Exec(
			"UPDATE material_words SET status = ?, updated_at = ? WHERE material_id = ? AND word = ?",
			status, now, materialID, word,
		)
	}
	return err
}

func (r *ReadingService) GetMarks(materialID string) ([]WordMark, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(
		"SELECT word, status FROM material_words WHERE material_id = ? ORDER BY word", materialID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WordMark
	for rows.Next() {
		var w WordMark
		if err := rows.Scan(&w.Word, &w.Status); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ------------------------------------------------------------
// Paragraph translation (inline, cached per paragraph index)
// ------------------------------------------------------------

func (r *ReadingService) TranslateParagraph(materialID string, paragraphIndex int, text string) (string, error) {
	if err := r.ensureDB(); err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	var exists int
	if err := r.db.QueryRow("SELECT COUNT(*) FROM materials WHERE id = ?", materialID).Scan(&exists); err != nil {
		return "", err
	}
	if exists == 0 {
		return "", fmt.Errorf("材料不存在")
	}
	// Cache hit?
	var cached string
	err := r.db.QueryRow(
		"SELECT translation FROM translations WHERE material_id = ? AND paragraph_index = ?",
		materialID, paragraphIndex,
	).Scan(&cached)
	if err == nil {
		return cached, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	// LLM call happens OUTSIDE the lock so other fast operations
	// (word marks, chat send) never block on a 10s network request.
	translation, err := r.dict.callLLM(
		translationSystemPrompt,
		"Translate the following English paragraph into natural, fluent Simplified Chinese. Output ONLY the Chinese translation, no explanation, no quotation marks:\n\n"+text,
		0.3, 1000,
	)
	if err != nil {
		return "", err
	}
	translation = strings.TrimSpace(translation)
	r.mu.Lock()
	defer r.mu.Unlock()
	// Re-check the cache: a concurrent call may have inserted it meanwhile.
	var cached2 string
	err = r.db.QueryRow(
		"SELECT translation FROM translations WHERE material_id = ? AND paragraph_index = ?",
		materialID, paragraphIndex,
	).Scan(&cached2)
	if err == nil {
		return cached2, nil
	}
	if _, err := r.db.Exec(
		"INSERT OR REPLACE INTO translations (material_id, paragraph_index, translation, created_at) VALUES (?, ?, ?, ?)",
		materialID, paragraphIndex, translation, time.Now().Unix(),
	); err != nil {
		return "", err
	}
	return translation, nil
}

func (r *ReadingService) GetTranslations(materialID string) ([]ParagraphTranslation, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(
		"SELECT paragraph_index, translation FROM translations WHERE material_id = ? ORDER BY paragraph_index",
		materialID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ParagraphTranslation
	for rows.Next() {
		var t ParagraphTranslation
		if err := rows.Scan(&t.ParagraphIndex, &t.Translation); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ------------------------------------------------------------
// Title auto-generation
// ------------------------------------------------------------

func (r *ReadingService) AutoTitle(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("内容不能为空")
	}
	sample := content
	if runes := []rune(sample); len(runes) > 500 {
		sample = string(runes[:500])
	}
	// maxTokens must be generous: reasoning-first models (e.g. deepseek-v4-flash)
	// consume a large thinking budget before writing the title. 60 was eaten
	// entirely by reasoning → empty result (finish_reason=length).
	title, err := r.dict.callLLM(
		"You are a helpful assistant. Given the beginning of an English article, produce one concise title of 3-10 words. Output ONLY the title text: no quotation marks, no trailing punctuation, no braces, no explanations.",
		"Article beginning:\n\n"+sample,
		0.5, 400,
	)
	if err != nil {
		return "", err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("模型未返回标题，请重试")
	}
	// Light cleanup: strip surrounding quotes and trailing punctuation.
	title = strings.Trim(title, " \"'“”‘’「」")
	title = strings.TrimRight(title, ".。!！?？·")
	title = strings.TrimSpace(title)
	return title, nil
}

// ------------------------------------------------------------
// Chat — ask questions about the material
// ------------------------------------------------------------

func (r *ReadingService) AskQuestion(materialID, question string) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", fmt.Errorf("问题不能为空")
	}
	if err := r.ensureDB(); err != nil {
		return "", err
	}
	var material ReadingMaterial
	err := r.db.QueryRow(
		"SELECT id, title, content, created_at, updated_at FROM materials WHERE id = ?", materialID,
	).Scan(&material.ID, &material.Title, &material.Content, &material.CreatedAt, &material.UpdatedAt)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("材料不存在")
	}
	if err != nil {
		return "", err
	}

	history := r.loadChatHistory(materialID)

	// Append-only history trimmed by token budget, NOT by turn count.
	// The material lives in the system message (stable prompt prefix) so
	// DeepSeek-style prefix caches keep hitting; trimming only happens when
	// the total estimate exceeds the budget, and it drops whole oldest
	// user+assistant pairs so the cache rebuilds once then stabilizes.
	// See reading-feature-design.md Q9.
	trimmed := trimChatHistory(material.Content, question, history, chatTokenBudget)

	messages := make([]map[string]interface{}, 0, len(trimmed)+2)
	messages = append(messages, map[string]interface{}{
		"role":    "system",
		"content": chatSystemPrompt + material.Content,
	})
	for _, m := range trimmed {
		messages = append(messages, map[string]interface{}{"role": m.Role, "content": m.Content})
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": question})

	// LLM call happens OUTSIDE the lock so other fast operations
	// (word marks, paragraph translate) never block on a 10s network request.
	answer, err := r.dict.callLLMMessages(messages, 0.7, 2000)
	if err != nil {
		return "", err
	}
	answer = strings.TrimSpace(answer)

	// Persist the Q&A pair only on success.
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixNano()
	qID := fmt.Sprintf("%d", now)
	aID := fmt.Sprintf("%d", now+1)
	if _, err := r.db.Exec(
		"INSERT INTO chat_messages (id, material_id, role, content, created_at) VALUES (?, ?, 'user', ?, ?)",
		qID, materialID, question, now/1e9,
	); err != nil {
		return "", err
	}
	if _, err := r.db.Exec(
		"INSERT INTO chat_messages (id, material_id, role, content, created_at) VALUES (?, ?, 'assistant', ?, ?)",
		aID, materialID, answer, now/1e9,
	); err != nil {
		return "", err
	}
	return answer, nil
}

func (r *ReadingService) GetChatHistory(materialID string) ([]ChatMsg, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	return r.loadChatHistory(materialID), nil
}

func (r *ReadingService) ClearChat(materialID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureDB(); err != nil {
		return err
	}
	_, err := r.db.Exec("DELETE FROM chat_messages WHERE material_id = ?", materialID)
	return err
}

func (r *ReadingService) loadChatHistory(materialID string) []ChatMsg {
	if r.db == nil {
		return nil
	}
	rows, err := r.db.Query(
		"SELECT role, content, created_at FROM chat_messages WHERE material_id = ? ORDER BY created_at ASC, rowid ASC",
		materialID,
	)
	if err != nil {
		log.Printf("ReadingService: loadChatHistory failed: %v", err)
		return nil
	}
	defer rows.Close()
	var out []ChatMsg
	for rows.Next() {
		var m ChatMsg
		if err := rows.Scan(&m.Role, &m.Content, &m.CreatedAt); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

// ------------------------------------------------------------
// Shared prompt constants
// ------------------------------------------------------------

const translationSystemPrompt = "You are a professional translator specializing in English-to-Chinese. You translate accurately and naturally, keeping the tone of the original text. You always output only the translation."

const chatSystemPrompt = `You are an English learning assistant. The user is reading the English material below and will ask questions about it (vocabulary, grammar, meaning, background, or translation).
Answer clearly and helpfully. Use the same language as the user's question (if they ask in Chinese, answer in Chinese; if in English, answer in English).
When explaining a word or phrase, give its meaning in the context of the material.

----- Material -----

`

// trimChatHistory drops whole oldest user+assistant pairs until the estimated
// total input (material + question + history) fits within the token budget.
// A lone leftover message is only dropped if it is still over budget.
func trimChatHistory(material, question string, history []ChatMsg, budget int) []ChatMsg {
	totalChars := len(material) + len(question)
	for _, m := range history {
		totalChars += len(m.Content)
	}
	trimmed := history
	for len(trimmed) >= 2 && totalChars/chatCharsPerToken > budget {
		totalChars -= len(trimmed[0].Content) + len(trimmed[1].Content)
		trimmed = trimmed[2:]
	}
	if len(trimmed) == 1 && totalChars/chatCharsPerToken > budget {
		trimmed = nil
	}
	return trimmed
}

// ------------------------------------------------------------
// Helpers
// ------------------------------------------------------------

// splitParagraphs splits pasted content into paragraphs:
// lines joined within a paragraph, blank lines separate paragraphs.
// The frontend must mirror this exact algorithm when rendering.
func splitParagraphs(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	var pars []string
	var cur []string
	flush := func() {
		t := strings.TrimSpace(strings.Join(cur, " "))
		if t != "" {
			pars = append(pars, t)
		}
		cur = cur[:0]
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, strings.TrimSpace(line))
	}
	flush()
	return pars
}

var wordRe = regexp.MustCompile(`[a-z]+(?:['’][a-z]+)*`)

// countEnglishWords returns the number of English words in a string.
func countEnglishWords(s string) int {
	return len(wordRe.FindAllString(strings.ToLower(s), -1))
}

// extractWordSet returns the set of lowercase words present in content.
func extractWordSet(content string) map[string]bool {
	set := make(map[string]bool)
	for _, m := range wordRe.FindAllString(strings.ToLower(content), -1) {
		set[m] = true
	}
	return set
}
