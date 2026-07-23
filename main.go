package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"wordwise/syncserver"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

func main() {
	ecdictSvc := &EcdictService{}
	historySvc := &HistoryService{}
	syncSvc := &SyncService{history: historySvc}

	// Wire up: HistoryService notifies SyncService on new entries
	historySvc.syncCb = syncSvc.OnEntryAdded

	app := application.New(application.Options{
		Name:        "WordWise",
		Description: "英语词典助手 - 系统托盘 + 全局快捷键 + ECDICT离线词典 + LLM智能查词 + 多设备同步",
		Services: []application.Service{
			application.NewService(&DictService{ecdict: ecdictSvc}),
			application.NewService(ecdictSvc),
			application.NewService(historySvc),
			application.NewService(syncSvc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	// ---- System Tray ----
	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip("WordWise - 英语词典助手 (Ctrl+Alt+Q 呼出)")

	trayMenu := app.NewMenu()
	trayMenu.Add("显示窗口").OnClick(func(ctx *application.Context) {
		w, ok := app.Window.Get("main-window")
		if ok {
			w.Show()
			w.Focus()
		}
	})
	trayMenu.AddSeparator()
	trayMenu.Add("退出").OnClick(func(ctx *application.Context) {
		app.Quit()
	})
	tray.SetMenu(trayMenu)

	tray.OnClick(func() {
		w, ok := app.Window.Get("main-window")
		if ok {
			if w.IsVisible() {
				w.Hide()
			} else {
				w.Show()
				w.Focus()
			}
		}
	})

	// ---- Main Window ----
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main-window",
		Title:            "WordWise - 英语词典助手",
		Width:            540,
		Height:           760,
		MinWidth:         420,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(30, 30, 46),
		URL:              "/",
		Windows: application.WindowsWindow{
			BackdropType: application.Acrylic,
		},
	})
	window.Center()

	// Hide on close instead of quit
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	// ---- Esc key to hide window ----
	app.Event.On("hide-window", func(e *application.CustomEvent) {
		window.Hide()
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// isEnglishText checks if the text looks like an English word or phrase
func isEnglishText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 200 {
		return false
	}
	pattern := regexp.MustCompile(`^[a-zA-Z][a-zA-Z\s\-']*[a-zA-Z]$|^[a-zA-Z]$`)
	if !pattern.MatchString(text) {
		return false
	}
	letters := 0
	for _, ch := range text {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			letters++
		}
	}
	return float64(letters)/float64(len(text)) > 0.6
}

// ============================================================
// EcdictService - offline ECDICT dictionary (SQLite)
// ============================================================

// EcdictEntry represents a row from the ECDICT database
type EcdictEntry struct {
	Word        string `json:"word"`
	Phonetic    string `json:"phonetic"`
	Definition  string `json:"definition"`  // English definitions (newline-separated)
	Translation string `json:"translation"` // Chinese definitions (newline-separated)
	Pos         string `json:"pos"`         // Part of speech
	Collins     *int   `json:"collins"`     // Collins star rating 1-5
	Oxford      *int   `json:"oxford"`      // Oxford 3000 flag
	Tag         string `json:"tag"`         // Exam tags: "cet4 cet6 gre toefl"
	Bnc         *int   `json:"bnc"`         // BNC frequency rank
	Frq         *int   `json:"frq"`         // Contemporary corpus frequency rank
	Exchange    string `json:"exchange"`    // Inflected forms: "d:abandoned/p:abandoned/i:abandoning/3:abandons"
}

type EcdictService struct {
	db   *sql.DB
	mu   sync.RWMutex
	once sync.Once
}

func (e *EcdictService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	e.once.Do(func() {
		dbPath := getEcdictDBPath()
		if _, err := os.Stat(dbPath); err == nil {
			e.openDB(dbPath)
		} else {
			log.Println("EcdictService: database not found at", dbPath, "- run import first")
		}
	})
	return nil
}

func (e *EcdictService) openDB(dbPath string) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Printf("EcdictService: failed to open DB: %v", err)
		return
	}
	db.SetMaxOpenConns(1) // SQLite single-writer
	e.mu.Lock()
	e.db = db
	e.mu.Unlock()
	log.Println("EcdictService: database opened successfully")
}

// LookupEcdict looks up a word in ECDICT. Returns nil if not found or DB unavailable.
func (e *EcdictService) LookupEcdict(word string) *EcdictEntry {
	e.mu.RLock()
	db := e.db
	e.mu.RUnlock()

	if db == nil {
		return nil
	}

	word = strings.TrimSpace(word)
	if word == "" {
		return nil
	}

	row := db.QueryRow(
		"SELECT word, phonetic, definition, translation, pos, collins, oxford, tag, bnc, frq, exchange FROM ecdict WHERE word = ? COLLATE NOCASE",
		word,
	)

	var entry EcdictEntry
	var phonetic, definition, translation, pos, tag, exchange sql.NullString
	var collins, oxford, bnc, frq sql.NullInt64

	err := row.Scan(
		&entry.Word, &phonetic, &definition, &translation, &pos,
		&collins, &oxford, &tag, &bnc, &frq, &exchange,
	)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("EcdictService: query error: %v", err)
		}
		return nil
	}

	entry.Phonetic = nullString(phonetic)
	entry.Definition = unescapeNewlines(nullString(definition))
	entry.Translation = unescapeNewlines(nullString(translation))
	entry.Pos = nullString(pos)
	entry.Tag = nullString(tag)
	entry.Exchange = nullString(exchange)
	entry.Collins = nullIntPtr(collins)
	entry.Oxford = nullIntPtr(oxford)
	entry.Bnc = nullIntPtr(bnc)
	entry.Frq = nullIntPtr(frq)

	return &entry
}

// EcdictIsAvailable checks if the ECDICT database is ready
func (e *EcdictService) EcdictIsAvailable() bool {
	e.mu.RLock()
	db := e.db
	e.mu.RUnlock()
	if db == nil {
		return false
	}
	var cnt int
	err := db.QueryRow("SELECT COUNT(*) FROM ecdict LIMIT 1").Scan(&cnt)
	return err == nil && cnt > 0
}

// ImportEcdict imports ECDICT CSV into SQLite database.
// Supports both .csv and .csv.gz formats. csvPath can be empty for auto-search.
// Uses batch multi-row INSERT for maximum throughput (~100K rows/sec).
func (e *EcdictService) ImportEcdict(csvPath string) error {
	if csvPath == "" {
		csvPath = findEcdictCSV()
	}
	// If relative path, resolve against executable directory
	if csvPath != "" && !filepath.IsAbs(csvPath) {
		if exeDir, err := getExeDir(); err == nil {
			absPath := filepath.Join(exeDir, csvPath)
			if _, err := os.Stat(absPath); err == nil {
				csvPath = absPath
			}
		}
		if !filepath.IsAbs(csvPath) {
			if wd, err := os.Getwd(); err == nil {
				absPath := filepath.Join(wd, csvPath)
				if _, err := os.Stat(absPath); err == nil {
					csvPath = absPath
				}
			}
		}
	}

	if csvPath == "" {
		return fmt.Errorf("未找到 ecdict.csv / ecdict.csv.gz，请将文件放到程序目录的 ECDICT/ 文件夹下，或输入完整路径")
	}
	if _, err := os.Stat(csvPath); err != nil {
		return fmt.Errorf("CSV文件不存在: %s", csvPath)
	}

	dbPath := getEcdictDBPath()
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建数据目录失败: %v", err)
	}

	// Close existing DB if open
	e.mu.Lock()
	if e.db != nil {
		e.db.Close()
		e.db = nil
	}
	e.mu.Unlock()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("创建数据库失败: %v", err)
	}
	defer db.Close()

	// SQLite performance pragmas
	if _, err := db.Exec("PRAGMA journal_mode=OFF"); err != nil {
		return fmt.Errorf("设置PRAGMA失败: %v", err)
	}
	if _, err := db.Exec("PRAGMA synchronous=OFF"); err != nil {
		return fmt.Errorf("设置PRAGMA失败: %v", err)
	}
	if _, err := db.Exec("PRAGMA cache_size=-65536"); err != nil { // 64MB cache
		log.Printf("Warning: set cache_size failed: %v", err)
	}

	// Create table
	if _, err := db.Exec(`DROP TABLE IF EXISTS ecdict`); err != nil {
		return fmt.Errorf("删除旧表失败: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE ecdict (
			word TEXT PRIMARY KEY,
			phonetic TEXT,
			definition TEXT,
			translation TEXT,
			pos TEXT,
			collins INTEGER,
			oxford INTEGER,
			tag TEXT,
			bnc INTEGER,
			frq INTEGER,
			exchange TEXT
		)
	`); err != nil {
		return fmt.Errorf("创建表失败: %v", err)
	}

	// Open CSV (auto-detect gzip from extension)
	f, err := os.Open(csvPath)
	if err != nil {
		return fmt.Errorf("打开CSV文件失败: %v", err)
	}
	defer f.Close()

	var csvInput io.Reader = f
	if strings.HasSuffix(strings.ToLower(csvPath), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("解压失败: %v", err)
		}
		defer gz.Close()
		csvInput = gz
	}

	reader := csv.NewReader(csvInput)
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1 // Allow variable fields

	// Skip header
	if _, err := reader.Read(); err != nil {
		return fmt.Errorf("读取CSV头部失败: %v", err)
	}

	const batchSize = 500 // rows per multi-row INSERT
	var batchArgs []any
	var batchBuilder strings.Builder
	totalRows := 0

	flushBatch := func() error {
		if len(batchArgs) == 0 {
			return nil
		}
		sql := "INSERT OR IGNORE INTO ecdict (word,phonetic,definition,translation,pos,collins,oxford,tag,bnc,frq,exchange) VALUES " + batchBuilder.String()
		_, err := db.Exec(sql, batchArgs...)
		if err != nil {
			log.Printf("Warning: batch insert error: %v", err)
		}
		batchBuilder.Reset()
		batchArgs = batchArgs[:0]
		return nil
	}

	// Wrap entire import in a single transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开始事务失败: %v", err)
	}

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if len(record) < 11 {
			continue
		}

		word := strings.TrimSpace(record[0])
		if word == "" {
			continue
		}

		// Build multi-row VALUES placeholder
		if batchBuilder.Len() > 0 {
			batchBuilder.WriteByte(',')
		}
		batchBuilder.WriteString("(?,?,?,?,?,?,?,?,?,?,?)")

		// Parse numeric fields
		var collinsVal, oxfordVal, bncVal, frqVal any
		if v, e := strconv.Atoi(strings.TrimSpace(record[5])); e == nil && v > 0 {
			collinsVal = v
		}
		if v, e := strconv.Atoi(strings.TrimSpace(record[6])); e == nil && v > 0 {
			oxfordVal = v
		}
		if v, e := strconv.Atoi(strings.TrimSpace(record[8])); e == nil && v > 0 {
			bncVal = v
		}
		if v, e := strconv.Atoi(strings.TrimSpace(record[9])); e == nil && v > 0 {
			frqVal = v
		}

		batchArgs = append(batchArgs,
			word,
			nullIfEmpty(record[1]),  // phonetic
			nullIfEmpty(record[2]),  // definition
			nullIfEmpty(record[3]),  // translation
			nullIfEmpty(record[4]),  // pos
			collinsVal,
			oxfordVal,
			nullIfEmpty(record[7]),  // tag
			bncVal,
			frqVal,
			nullIfEmpty(record[10]), // exchange
		)

		totalRows++

		// Flush batch when full
		if len(batchArgs) >= batchSize*11 {
			if err := flushBatch(); err != nil {
				tx.Rollback()
				return err
			}
			if totalRows%50000 == 0 {
				log.Printf("  Imported %d rows...", totalRows)
			}
		}
	}

	// Flush remaining
	if err := flushBatch(); err != nil {
		tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %v", err)
	}

	// Create index after data load (much faster than incremental)
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_ecdict_word ON ecdict(word)"); err != nil {
		log.Printf("Warning: create index failed: %v", err)
	}

	log.Printf("ECDICT import complete: %d rows", totalRows)

	// Re-open DB for queries
	e.openDB(dbPath)

	return nil
}

// ============================================================
// DictService - ECDICT fast lookup + LLM enrichment
// ============================================================

type DictService struct {
	app         *application.App
	apiKey      string
	apiURL      string
	modelName   string
	shortcutKey string
	ready       bool
	ecdict      *EcdictService
}

func (d *DictService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	d.app = application.Get()

	configPath := getConfigPath("config.json")
	data, err := os.ReadFile(configPath)
	if err == nil {
		var cfg map[string]string
		if json.Unmarshal(data, &cfg) == nil {
			if v, ok := cfg["apiKey"]; ok {
				d.apiKey = v
			}
			if v, ok := cfg["apiURL"]; ok && v != "" {
				d.apiURL = v
			}
			if v, ok := cfg["modelName"]; ok && v != "" {
				d.modelName = v
			}
			if v, ok := cfg["shortcutKey"]; ok && v != "" {
				d.shortcutKey = v
			}
		}
	}
	if d.apiURL == "" {
		d.apiURL = "https://api.openai.com/v1/chat/completions"
	}
	if d.modelName == "" {
		d.modelName = "gpt-4o-mini"
	}
	if d.shortcutKey == "" {
		d.shortcutKey = "Ctrl+Alt+Q"
	}
	log.Println("DictService started, model:", d.modelName, "shortcut:", d.shortcutKey)
	d.ready = true
	d.registerShortcut()
	return nil
}

// LookupWordFast returns ECDICT result immediately (offline, ~10ms).
// Returns JSON string of the ECDICT entry, or empty string if not found.
func (d *DictService) LookupWordFast(word string) string {
	if d.ecdict == nil {
		return ""
	}
	entry := d.ecdict.LookupEcdict(word)
	if entry == nil {
		return ""
	}
	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("LookupWordFast: marshal error: %v", err)
		return ""
	}
	return string(data)
}

// LookupWordLLM calls LLM to get enriched word definition.
// This is the slow path (~2-10s), called after ECDICT fast result is shown.
func (d *DictService) LookupWordLLM(word string) (string, error) {
	if !d.ready {
		return "", fmt.Errorf("服务正在初始化，请稍后重试")
	}
	if d.apiKey == "" {
		return "", fmt.Errorf("请先设置 API Key（点击 ⚙️ 设置按钮）")
	}
	word = strings.TrimSpace(word)
	if word == "" {
		return "", fmt.Errorf("请输入要查询的单词或短语")
	}

	// Build context-aware prompt: if ECDICT has data, include it for richer LLM output
	ecdictInfo := ""
	if d.ecdict != nil {
		if entry := d.ecdict.LookupEcdict(word); entry != nil {
			parts := []string{}
			if entry.Phonetic != "" {
				parts = append(parts, "音标: "+entry.Phonetic)
			}
			if entry.Translation != "" {
				parts = append(parts, "中文释义: "+entry.Translation)
			}
			if entry.Tag != "" {
				parts = append(parts, "考试标签: "+entry.Tag)
			}
			if len(parts) > 0 {
				ecdictInfo = "\n\n已知基础信息（ECDICT离线词典）：\n" + strings.Join(parts, "\n") + "\n\n请在以上基础上补充更丰富的内容，避免重复已有信息。"
			}
		}
	}

	prompt := fmt.Sprintf(`请对英语单词或短语「%s」进行详细解释，严格按照以下JSON格式返回（不要包含markdown代码块标记）：

{
  "word": "%s",
  "phonetic": "音标（国际音标）",
  "pronunciation": "发音提示（用中文近似标注）",
  "definitions": [
    {
      "pos": "词性（如 n. / v. / adj. 等）",
      "meaning": "中文释义",
      "english_example": "英文例句",
      "chinese_example": "例句中文翻译"
    }
  ],
  "memory_tips": "帮助记忆的技巧、词根词缀分析、联想记忆等",
  "synonyms": "近义词（如有）",
  "antonyms": "反义词（如有）",
  "etymology": "词源小故事（简短有趣）"
}

请确保返回纯JSON，不要有其他内容。如果有多个词性，请在definitions中分别列出。%s`, word, word, ecdictInfo)

	reqBody := map[string]interface{}{
		"model": d.modelName,
		"messages": []map[string]string{
			{"role": "system", "content": "你是一个专业的英语词典助手，总是以纯JSON格式回复，不包含markdown标记。用户会给你一个英语单词或短语，你必须解释它。"},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.3,
		"max_tokens":  2000,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("构建请求失败: %v", err)
	}

	// Build the actual request URL
	apiURL := d.apiURL
	if !strings.HasSuffix(apiURL, "/chat/completions") {
		apiURL = strings.TrimRight(apiURL, "/") + "/chat/completions"
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %v", err)
	}

	if resp.StatusCode != 200 {
		log.Printf("API error: status=%d url=%s body=%s", resp.StatusCode, apiURL, string(body))
		var errResp map[string]interface{}
		if json.Unmarshal(body, &errResp) == nil {
			if e, ok := errResp["error"].(map[string]interface{}); ok {
				if msg, ok := e["message"].(string); ok {
					return "", fmt.Errorf("API错误: %s", msg)
				}
			}
			if msg, ok := errResp["message"].(string); ok {
				if code, ok := errResp["code"]; ok {
					return "", fmt.Errorf("API错误(code=%v): %s", code, msg)
				}
				return "", fmt.Errorf("API错误: %s", msg)
			}
		}
		return "", fmt.Errorf("API返回状态码 %d, 响应: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", fmt.Errorf("解析响应失败: %v", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("API未返回有效内容")
	}

	content := chatResp.Choices[0].Message.Content
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	if idx := strings.Index(content, "{"); idx >= 0 {
		if idx > 0 {
			content = content[idx:]
		}
	}
	if idx := strings.LastIndex(content, "}"); idx >= 0 {
		content = content[:idx+1]
	}
	var b strings.Builder
	for _, ch := range content {
		if ch >= 0x20 || ch == '\n' || ch == '\r' || ch == '\t' {
			b.WriteRune(ch)
		}
	}
	content = strings.TrimSpace(b.String())
	return content, nil
}

// LookupWord is the legacy combined method (kept for backward compat).
// It first tries ECDICT, then LLM, returning the merged result.
func (d *DictService) LookupWord(word string) (string, error) {
	return d.LookupWordLLM(word)
}

// SaveConfig saves API configuration
func (d *DictService) SaveConfig(apiKey, apiURL, modelName, shortcutKey string) error {
	d.apiKey = apiKey
	if apiURL != "" {
		d.apiURL = apiURL
	}
	if modelName != "" {
		d.modelName = modelName
	}
	oldShortcut := d.shortcutKey
	if shortcutKey != "" {
		d.shortcutKey = shortcutKey
	}
	configPath := getConfigPath("config.json")
	data, _ := json.MarshalIndent(map[string]string{
		"apiKey":      apiKey,
		"apiURL":      d.apiURL,
		"modelName":   d.modelName,
		"shortcutKey": d.shortcutKey,
	}, "", "  ")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return err
	}
	if oldShortcut != d.shortcutKey && d.app != nil {
		if err := d.app.GlobalShortcut.Unregister(oldShortcut); err != nil {
			log.Printf("unregister old shortcut %s failed: %v", oldShortcut, err)
		}
		d.registerShortcut()
	}
	return nil
}

// GetConfig returns current configuration
func (d *DictService) GetConfig() map[string]string {
	if !d.ready {
		return map[string]string{}
	}
	return map[string]string{
		"apiKey":      d.apiKey,
		"apiURL":      d.apiURL,
		"modelName":   d.modelName,
		"shortcutKey": d.shortcutKey,
	}
}

// ReadClipboard reads text from system clipboard
func (d *DictService) ReadClipboard() string {
	if d.app == nil {
		return ""
	}
	text, ok := d.app.Clipboard.Text()
	if !ok {
		return ""
	}
	return text
}

// IsEnglishText checks if text is English word/phrase
func (d *DictService) IsEnglishText(text string) bool {
	return isEnglishText(text)
}

// registerShortcut registers the current global shortcut key
func (d *DictService) registerShortcut() {
	if d.app == nil || d.shortcutKey == "" {
		return
	}
	err := d.app.GlobalShortcut.Register(d.shortcutKey, func() {
		application.InvokeSync(func() {
			w, ok := d.app.Window.Get("main-window")
			if ok {
				if !w.IsVisible() {
					w.Show()
				}
				w.Focus()
			}
			text, ok := d.app.Clipboard.Text()
			if ok && isEnglishText(text) {
				word := strings.TrimSpace(text)
				d.app.Event.Emit("clipboard-english-detected", word)
			}
		})
	})
	if err != nil {
		log.Printf("register shortcut %s failed: %v", d.shortcutKey, err)
	} else {
		log.Printf("registered shortcut: %s", d.shortcutKey)
	}
}

// ============================================================
// HistoryEntry represents a saved word with its full lookup result.
// This is the core data model for the local word book and sync.
type HistoryEntry struct {
	ID        string `json:"id"`        // Unique ID (nanosecond timestamp)
	Word      string `json:"word"`      // The looked-up word
	Result    string `json:"result"`    // JSON string of merged ECDICT+LLM result
	CreatedAt int64  `json:"createdAt"` // Unix timestamp (seconds)
	UpdatedAt int64  `json:"updatedAt"` // Unix timestamp (seconds)
}

// HistoryService manages the local word book using SQLite.
// It replaces the old JSON-file-based history with proper database storage,
// enabling reliable persistence and sync support.
type HistoryService struct {
	db     *sql.DB
	mu     sync.RWMutex
	once   sync.Once
	syncCb func(entry HistoryEntry) // callback to notify sync service
}

func (h *HistoryService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	h.once.Do(func() {
		if err := h.openDB(); err != nil {
			log.Printf("HistoryService: failed to open DB: %v", err)
		}
	})
	return nil
}

func (h *HistoryService) openDB() error {
	dbPath := getHistoryDBPath()
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建数据目录失败: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("打开数据库失败: %v", err)
	}

	// SQLite pragmas
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		log.Printf("Warning: set WAL mode failed: %v", err)
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		log.Printf("Warning: set synchronous failed: %v", err)
	}

	// Create table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS history (
			id         TEXT PRIMARY KEY,
			word       TEXT NOT NULL,
			result     TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		return fmt.Errorf("创建 history 表失败: %v", err)
	}

	// Index for word lookup
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_history_word ON history(word)"); err != nil {
		log.Printf("Warning: create index failed: %v", err)
	}

	// Index for time-ordered queries
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_history_created ON history(created_at DESC)"); err != nil {
		log.Printf("Warning: create index failed: %v", err)
	}

	// Migrate from old JSON history if it exists and DB is empty
	h.migrateFromJSON(db)

	h.mu.Lock()
	h.db = db
	h.mu.Unlock()

	log.Println("HistoryService: database opened successfully")
	return nil
}

// migrateFromJSON imports old history.json data into SQLite (one-time migration)
func (h *HistoryService) migrateFromJSON(db *sql.DB) {
	jsonPath := getConfigPath("history.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return // No old file, nothing to migrate
	}

	var oldEntries []struct {
		ID        string `json:"id"`
		Word      string `json:"word"`
		Result    string `json:"result"`
		CreatedAt string `json:"createdAt"`
	}
	if err := json.Unmarshal(data, &oldEntries); err != nil {
		return
	}

	// Check if DB already has data
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM history").Scan(&count); err != nil || count > 0 {
		return // DB already has data, skip migration
	}

	tx, err := db.Begin()
	if err != nil {
		return
	}

	migrated := 0
	for _, e := range oldEntries {
		if e.ID == "" || e.Word == "" {
			continue
		}
		// Parse the old time format
		var createdAt int64
		if t, err := time.Parse("2006-01-02 15:04:05", e.CreatedAt); err == nil {
			createdAt = t.Unix()
		} else {
			createdAt = time.Now().Unix()
		}

		_, err := tx.Exec(
			"INSERT OR IGNORE INTO history (id, word, result, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
			e.ID, e.Word, e.Result, createdAt, createdAt,
		)
		if err == nil {
			migrated++
		}
	}

	if err := tx.Commit(); err != nil {
		return
	}

	if migrated > 0 {
		log.Printf("HistoryService: migrated %d entries from history.json", migrated)
		// Rename old file as backup
		os.Rename(jsonPath, jsonPath+".bak")
	}
}

// GetDB returns the underlying database connection (for sync service use)
func (h *HistoryService) GetDB() *sql.DB {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.db
}

// GetHistory returns all entries (newest first)
func (h *HistoryService) GetHistory() []HistoryEntry {
	h.mu.RLock()
	db := h.db
	h.mu.RUnlock()

	if db == nil {
		return []HistoryEntry{}
	}

	rows, err := db.Query("SELECT id, word, result, created_at, updated_at FROM history ORDER BY created_at DESC")
	if err != nil {
		log.Printf("HistoryService: query error: %v", err)
		return []HistoryEntry{}
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.ID, &e.Word, &e.Result, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

// AddHistory adds a new entry or updates an existing one (same word)
func (h *HistoryService) AddHistory(word, result string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.db == nil {
		return fmt.Errorf("数据库未初始化")
	}

	now := time.Now().Unix()

	var savedEntry HistoryEntry

	// Check if word already exists
	var existingID string
	err := h.db.QueryRow("SELECT id FROM history WHERE word = ? COLLATE NOCASE", word).Scan(&existingID)

	if err == sql.ErrNoRows {
		// New entry
		id := fmt.Sprintf("%d", time.Now().UnixNano())
		_, err := h.db.Exec(
			"INSERT INTO history (id, word, result, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
			id, word, result, now, now,
		)
		if err != nil {
			return err
		}
		savedEntry = HistoryEntry{ID: id, Word: word, Result: result, CreatedAt: now, UpdatedAt: now}
	} else if err == nil {
		// Update existing entry
		_, err := h.db.Exec(
			"UPDATE history SET result = ?, updated_at = ? WHERE id = ?",
			result, now, existingID,
		)
		if err != nil {
			return err
		}
		savedEntry = HistoryEntry{ID: existingID, Word: word, Result: result, CreatedAt: now, UpdatedAt: now}
	} else {
		return err
	}

	// Notify sync service (non-blocking)
	if h.syncCb != nil {
		h.syncCb(savedEntry)
	}

	return nil
}

// DeleteHistory removes an entry by ID
func (h *HistoryService) DeleteHistory(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.db == nil {
		return fmt.Errorf("数据库未初始化")
	}

	_, err := h.db.Exec("DELETE FROM history WHERE id = ?", id)
	return err
}

// ClearHistory removes all entries
func (h *HistoryService) ClearHistory() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.db == nil {
		return fmt.Errorf("数据库未初始化")
	}

	_, err := h.db.Exec("DELETE FROM history")
	return err
}

// GetHistoryEntry returns a single entry by ID
func (h *HistoryService) GetHistoryEntry(id string) *HistoryEntry {
	h.mu.RLock()
	db := h.db
	h.mu.RUnlock()

	if db == nil {
		return nil
	}

	var e HistoryEntry
	err := db.QueryRow(
		"SELECT id, word, result, created_at, updated_at FROM history WHERE id = ?",
		id,
	).Scan(&e.ID, &e.Word, &e.Result, &e.CreatedAt, &e.UpdatedAt)

	if err != nil {
		return nil
	}
	return &e
}

// GetAllEntriesForSync returns all entries formatted for sync push
func (h *HistoryService) GetAllEntriesForSync() []HistoryEntry {
	h.mu.RLock()
	db := h.db
	h.mu.RUnlock()

	if db == nil {
		return []HistoryEntry{}
	}

	rows, err := db.Query("SELECT id, word, result, created_at, updated_at FROM history ORDER BY updated_at ASC")
	if err != nil {
		return []HistoryEntry{}
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.ID, &e.Word, &e.Result, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

// ============================================================
// SyncService - PC端同步客户端（连接远程同步服务器）
// ============================================================

type SyncService struct {
	history   *HistoryService
	syncAddr  string // 远程同步服务器地址，如 http://your-server:9274
	syncToken string // 用户Token（由服务器分配）
	autoSync  bool   // 是否启用自动同步
}

func (s *SyncService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.loadConfig()
	// Auto-pull on startup (background, non-blocking)
	if s.autoSync && s.syncAddr != "" && s.syncToken != "" {
		go func() {
			msg, err := s.pullFromServerSilent()
			if err != nil {
				log.Printf("SyncService: 启动自动拉取失败: %v", err)
			} else if msg != "" {
				log.Printf("SyncService: 启动自动拉取: %s", msg)
			}
		}()
	}
	return nil
}

func (s *SyncService) loadConfig() {
	configPath := getConfigPath("sync_config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	var cfg struct {
		SyncAddr  string `json:"syncAddr"`
		SyncToken string `json:"syncToken"`
		AutoSync  bool   `json:"autoSync"`
	}
	if json.Unmarshal(data, &cfg) == nil {
		s.syncAddr = cfg.SyncAddr
		s.syncToken = cfg.SyncToken
		s.autoSync = cfg.AutoSync
	}
}

func (s *SyncService) saveConfig() error {
	configPath := getConfigPath("sync_config.json")
	data, _ := json.MarshalIndent(map[string]interface{}{
		"syncAddr":  s.syncAddr,
		"syncToken": s.syncToken,
		"autoSync":  s.autoSync,
	}, "", "  ")
	return os.WriteFile(configPath, data, 0644)
}

// GetSyncConfig returns current sync configuration
func (s *SyncService) GetSyncConfig() map[string]string {
	autoSyncStr := "false"
	if s.autoSync {
		autoSyncStr = "true"
	}
	return map[string]string{
		"syncAddr":  s.syncAddr,
		"syncToken": s.syncToken,
		"autoSync":  autoSyncStr,
	}
}

// SaveSyncConfig saves sync configuration
func (s *SyncService) SaveSyncConfig(syncAddr, syncToken string, autoSync bool) error {
	s.syncAddr = syncAddr
	s.syncToken = syncToken
	s.autoSync = autoSync
	return s.saveConfig()
}

// TestConnection tests the connection to the sync server.
// Returns a human-readable status message.
func (s *SyncService) TestConnection() (string, error) {
	if s.syncAddr == "" {
		return "", fmt.Errorf("请先设置同步服务器地址")
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/health"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("无法连接服务器: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("服务器返回异常状态: HTTP %d", resp.StatusCode)
	}

	// If token is set, also check user status
	if s.syncToken != "" {
		statusURL := strings.TrimRight(s.syncAddr, "/") + "/api/v1/user/status"
		req, _ := http.NewRequest("GET", statusURL, nil)
		req.Header.Set("Authorization", "Bearer "+s.syncToken)
		resp2, err := client.Do(req)
		if err != nil {
			return "服务器可达，但Token验证失败", nil
		}
		defer resp2.Body.Close()

		if resp2.StatusCode == 200 {
			var status syncserver.SyncStatusResponse
			if json.NewDecoder(resp2.Body).Decode(&status) == nil {
				return fmt.Sprintf("✅ 连接成功！已同步 %d 个单词", status.WordCount), nil
			}
		}
		return "⚠️ 服务器可达，但Token无效（可能需要重新获取Token）", nil
	}

	return "✅ 服务器连接正常，请设置Token后开始同步", nil
}

// CreateUser creates a new user on the remote server and returns the token.
func (s *SyncService) CreateUser() (string, error) {
	if s.syncAddr == "" {
		return "", fmt.Errorf("请先设置同步服务器地址")
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/user/create"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader("{}"))
	if err != nil {
		return "", fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("创建用户失败 (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Token   string `json:"token"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("解析响应失败: %v", err)
	}

	// Auto-save the token
	s.syncToken = result.Token
	s.saveConfig()

	return result.Token, nil
}

// PushToServer pushes all local history entries to the remote sync server.
func (s *SyncService) PushToServer() (string, error) {
	if s.syncAddr == "" || s.syncToken == "" {
		return "", fmt.Errorf("请先配置同步服务器地址和Token")
	}
	if s.history == nil {
		return "", fmt.Errorf("历史服务未初始化")
	}

	entries := s.history.GetAllEntriesForSync()
	if len(entries) == 0 {
		return "没有可同步的数据", nil
	}

	// Convert to sync entries
	syncEntries := make([]syncserver.SyncEntry, len(entries))
	for i, e := range entries {
		syncEntries[i] = syncserver.SyncEntry{
			ID:        e.ID,
			Word:      e.Word,
			Result:    e.Result,
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.UpdatedAt,
		}
	}

	reqBody := syncserver.SyncPushRequest{Entries: syncEntries}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("构建请求失败: %v", err)
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/sync/push"
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.syncToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("Token无效，请重新获取")
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("服务器返回错误: HTTP %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if json.NewDecoder(resp.Body).Decode(&result) == nil {
		if upserted, ok := result["upserted"]; ok {
			return fmt.Sprintf("成功推送 %v 条记录", upserted), nil
		}
	}

	return "推送完成", nil
}

// PullFromServer pulls entries from the remote sync server and merges into local history.
func (s *SyncService) PullFromServer() (string, error) {
	if s.syncAddr == "" || s.syncToken == "" {
		return "", fmt.Errorf("请先配置同步服务器地址和Token")
	}
	if s.history == nil {
		return "", fmt.Errorf("历史服务未初始化")
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/sync/pull"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.syncToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("Token无效，请重新获取")
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("服务器返回错误: HTTP %d", resp.StatusCode)
	}

	var result syncserver.SyncPullResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("解析响应失败: %v", err)
	}

	if len(result.Entries) == 0 {
		return "服务器上没有新数据", nil
	}

	// Merge into local history
	merged := 0
	for _, e := range result.Entries {
		if e.Deleted {
			s.history.DeleteHistory(e.ID)
			continue
		}
		s.history.AddHistory(e.Word, e.Result)
		merged++
	}

	return fmt.Sprintf("成功拉取并合并 %d 条记录", merged), nil
}

// OnEntryAdded is called by HistoryService when a new entry is saved.
// If auto-sync is enabled, it pushes the entry to the server in the background.
func (s *SyncService) OnEntryAdded(entry HistoryEntry) {
	if !s.autoSync || s.syncAddr == "" || s.syncToken == "" {
		return
	}
	go s.pushEntryAsync(entry)
}

// pushEntryAsync pushes a single entry to the server in the background.
func (s *SyncService) pushEntryAsync(entry HistoryEntry) {
	syncEntry := syncserver.SyncEntry{
		ID:        entry.ID,
		Word:      entry.Word,
		Result:    entry.Result,
		CreatedAt: entry.CreatedAt,
		UpdatedAt: entry.UpdatedAt,
	}

	reqBody := syncserver.SyncPushRequest{Entries: []syncserver.SyncEntry{syncEntry}}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("SyncService: auto-push marshal error: %v", err)
		return
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/sync/push"
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		log.Printf("SyncService: auto-push request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.syncToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("SyncService: auto-push failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		log.Printf("SyncService: auto-push token invalid")
		return
	}
	if resp.StatusCode != 200 {
		log.Printf("SyncService: auto-push server error: HTTP %d", resp.StatusCode)
		return
	}

	log.Printf("SyncService: auto-pushed '%s' to server", entry.Word)
}

// pullFromServerSilent pulls entries from server without returning user-facing messages.
// Used for auto-sync on startup.
func (s *SyncService) pullFromServerSilent() (string, error) {
	if s.syncAddr == "" || s.syncToken == "" {
		return "", fmt.Errorf("未配置")
	}
	if s.history == nil {
		return "", fmt.Errorf("历史服务未初始化")
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/sync/pull"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.syncToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("Token无效")
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var result syncserver.SyncPullResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Entries) == 0 {
		return "", nil
	}

	merged := 0
	for _, e := range result.Entries {
		if e.Deleted {
			s.history.DeleteHistory(e.ID)
			continue
		}
		s.history.AddHistory(e.Word, e.Result)
		merged++
	}

	return fmt.Sprintf("拉取 %d 条记录", merged), nil
}

// ============================================================
// Utility
// ============================================================

func getConfigPath(filename string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	dir = filepath.Join(dir, "WordWise")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, filename)
}

func getHistoryDBPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	dir = filepath.Join(dir, "WordWise")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "history.db")
}

func getEcdictDBPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	dir = filepath.Join(dir, "WordWise")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "ecdict.db")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func unescapeNewlines(val string) string {
	if val == "" {
		return val
	}
	return strings.ReplaceAll(val, `\n`, "\n")
}

func nullString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func nullIntPtr(ni sql.NullInt64) *int {
	if ni.Valid {
		v := int(ni.Int64)
		return &v
	}
	return nil
}

func nullIfEmpty(s string) interface{} {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}

// getExeDir returns the directory of the running executable.
func getExeDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// findEcdictCSV searches common locations for ecdict.csv or ecdict.csv.gz.
// Returns absolute path if found, empty string if not.
// Prefers .csv over .csv.gz when both exist (faster import).
func findEcdictCSV() string {
	type candidate struct{ dir, file string }
	dirs := []string{}

	if exeDir, err := getExeDir(); err == nil {
		dirs = append(dirs, exeDir)
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd)
	}
	if configDir, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(configDir, "WordWise"))
	}

	// Search .csv first (faster), then .csv.gz
	for _, name := range []string{"ECDICT/ecdict.csv", "ecdict.csv", "ECDICT/ecdict.csv.gz", "ecdict.csv.gz"} {
		for _, dir := range dirs {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				abs, _ := filepath.Abs(p)
				log.Printf("EcdictService: found CSV at %s", abs)
				return abs
			}
		}
	}

	log.Printf("EcdictService: CSV not found, searched dirs: %v", dirs)
	return ""
}
