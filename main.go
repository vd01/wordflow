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
	"os/exec"
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
			application.NewService(&DictService{ecdict: ecdictSvc, history: historySvc}),
			application.NewService(ecdictSvc),
			application.NewService(historySvc),
			application.NewService(syncSvc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		// Single-instance lock: prevents a second process from starting
		// (which would otherwise create a duplicate system-tray icon).
		// On a second launch attempt we just surface the existing window.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.wordwise.app",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				if w, ok := application.Get().Window.Get("main-window"); ok {
					w.Show()
					w.Focus()
				}
			},
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
		Width:            600,
		Height:           820,
		MinWidth:         460,
		MinHeight:        640,
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

// ============================================================
// lruCache - simple bounded LRU cache with O(1) get/set
// ============================================================

type lruEntry struct {
	key  string
	val  string
	prev *lruEntry
	next *lruEntry
}

type lruCache struct {
	maxSize int
	size    int
	head    *lruEntry // most recently used
	tail    *lruEntry // least recently used
	items   map[string]*lruEntry
	mu      sync.RWMutex
}

func newLRUCache(maxSize int) *lruCache {
	return &lruCache{
		maxSize: maxSize,
		items:   make(map[string]*lruEntry, maxSize),
	}
}

func (c *lruCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.moveToFront(e)
		return e.val, true
	}
	return "", false
}

func (c *lruCache) set(key, val string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		e.val = val
		c.moveToFront(e)
		return
	}
	e := &lruEntry{key: key, val: val}
	c.items[key] = e
	c.addToFront(e)
	c.size++
	if c.size > c.maxSize {
		c.evictTail()
	}
}

func (c *lruCache) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.size
}

func (c *lruCache) moveToFront(e *lruEntry) {
	if c.head == e {
		return
	}
	c.remove(e)
	c.addToFront(e)
}

func (c *lruCache) addToFront(e *lruEntry) {
	e.prev = nil
	e.next = c.head
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
}

func (c *lruCache) remove(e *lruEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		c.tail = e.prev
	}
}

func (c *lruCache) evictTail() {
	if c.tail == nil {
		return
	}
	delete(c.items, c.tail.key)
	c.remove(c.tail)
	c.size--
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

	// Performance pragmas for fast read-only lookups
	pragmas := []string{
		"PRAGMA journal_mode=OFF",    // No WAL overhead for read-only
		"PRAGMA synchronous=OFF",     // No fsync on reads
		"PRAGMA cache_size=-8192",    // 8MB page cache
		"PRAGMA mmap_size=33554432",  // 32MB memory-mapped I/O
		"PRAGMA page_size=4096",      // Standard page size
		"PRAGMA busy_timeout=5000",   // Wait up to 5s for lock
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			log.Printf("EcdictService: pragma warning: %v", err)
		}
	}

	e.mu.Lock()
	e.db = db
	e.mu.Unlock()
	log.Println("EcdictService: database opened successfully")
}

// LookupEcdict looks up a word in ECDICT. Returns nil if not found or DB unavailable.
// Uses case-insensitive matching by trying the lowercase form first (index-friendly),
// then falls back to COLLATE NOCASE only if needed.
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

	// Try exact match first (uses primary key index — fastest)
	entry := e.queryWord(db, word)
	if entry != nil {
		return entry
	}

	// Try lowercase match (covers most case mismatches, still index-friendly)
	lowerWord := strings.ToLower(word)
	if lowerWord != word {
		entry = e.queryWord(db, lowerWord)
		if entry != nil {
			return entry
		}
	}

	// Last resort: COLLATE NOCASE (slower, but catches mixed-case entries)
	return e.queryWordCollate(db, word)
}

func (e *EcdictService) queryWord(db *sql.DB, word string) *EcdictEntry {
	row := db.QueryRow(
		"SELECT word, phonetic, definition, translation, pos, collins, oxford, tag, bnc, frq, exchange FROM ecdict WHERE word = ?",
		word,
	)
	return e.scanEntry(row)
}

func (e *EcdictService) queryWordCollate(db *sql.DB, word string) *EcdictEntry {
	row := db.QueryRow(
		"SELECT word, phonetic, definition, translation, pos, collins, oxford, tag, bnc, frq, exchange FROM ecdict WHERE word = ? COLLATE NOCASE",
		word,
	)
	return e.scanEntry(row)
}

func (e *EcdictService) scanEntry(row *sql.Row) *EcdictEntry {
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
	if _, err := db.Exec("PRAGMA cache_size=-8192"); err != nil { // 8MB cache
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
// PromptConfig - user-customizable LLM prompt configuration
// ============================================================

// PromptField defines a single field the LLM should return and the UI should render.
type PromptField struct {
	Key     string `json:"key"`     // JSON key in LLM response & merged data
	Label   string `json:"label"`   // Display name in UI
	Icon    string `json:"icon"`    // Emoji icon for the section
	Type    string `json:"type"`    // "string" | "text" | "list" | "definitions"
	Desc    string `json:"desc"`    // Guidance: shown in UI AND injected into prompt schema (except definitions)
	Enabled bool   `json:"enabled"` // Whether included in prompt & rendered
	Builtin bool   `json:"builtin"` // Built-in field (key locked, not deletable)
}

// PromptConfig is the full user-editable prompt configuration.
type PromptConfig struct {
	SystemPrompt      string         `json:"systemPrompt"`
	Fields            []PromptField `json:"fields"`
	ExtraRequirements string         `json:"extraRequirements"`
	Temperature       float64       `json:"temperature"`
	MaxTokens         int           `json:"maxTokens"`
}

// definitionsSchemaBlock is the hardcoded JSON schema fragment for the definitions array.
const definitionsSchemaBlock = `  "definitions": [
    {
      "pos": "词性（如 n. / v. / adj. 等）",
      "meaning": "中文释义",
      "english_example": "英文例句",
      "chinese_example": "例句中文翻译"
    }
  ]`

// defaultPromptConfig returns the built-in default prompt configuration.
func defaultPromptConfig() *PromptConfig {
	return &PromptConfig{
		SystemPrompt:      "你是一个专业的英语词典助手，总是以纯JSON格式回复，不包含markdown标记。用户会给你一个英语单词或短语，你必须解释它。",
		ExtraRequirements: "",
		Temperature:       0.3,
		MaxTokens:         2000,
		Fields: []PromptField{
			{Key: "word", Label: "单词", Icon: "🔤", Type: "string", Desc: "被查询的英语单词或短语", Enabled: true, Builtin: true},
			{Key: "phonetic", Label: "音标", Icon: "🎵", Type: "string", Desc: "音标（国际音标）", Enabled: true, Builtin: true},
			{Key: "pronunciation", Label: "发音提示", Icon: "🗣️", Type: "string", Desc: "发音提示（用中文近似标注）", Enabled: true, Builtin: true},
			{Key: "definitions", Label: "详细释义", Icon: "📖", Type: "definitions", Desc: "包含词性、释义、英文例句及中文翻译", Enabled: true, Builtin: true},
			{Key: "memory_tips", Label: "记忆技巧", Icon: "🧠", Type: "text", Desc: "帮助记忆的技巧、词根词缀分析、联想记忆等", Enabled: true, Builtin: true},
			{Key: "synonyms", Label: "近义词", Icon: "📌", Type: "list", Desc: "近义词（如有）", Enabled: true, Builtin: true},
			{Key: "antonyms", Label: "反义词", Icon: "🚫", Type: "list", Desc: "反义词（如有）", Enabled: true, Builtin: true},
			{Key: "etymology", Label: "词源", Icon: "📚", Type: "text", Desc: "词源小故事（简短有趣）", Enabled: true, Builtin: true},
		},
	}
}

// fieldEnabled reports whether a field with the given key is enabled.
func (c *PromptConfig) fieldEnabled(key string) bool {
	for _, f := range c.Fields {
		if f.Key == key {
			return f.Enabled
		}
	}
	return false
}

// mergePromptConfig overlays a saved config onto the defaults so newly-added
// built-in fields appear for existing users while preserving their customizations.
func mergePromptConfig(saved *PromptConfig) *PromptConfig {
	def := defaultPromptConfig()
	if saved == nil {
		return def
	}
	result := &PromptConfig{
		SystemPrompt:      saved.SystemPrompt,
		ExtraRequirements: saved.ExtraRequirements,
		Temperature:       saved.Temperature,
		MaxTokens:         saved.MaxTokens,
		Fields:            []PromptField{},
	}
	if result.SystemPrompt == "" {
		result.SystemPrompt = def.SystemPrompt
	}
	if result.Temperature <= 0 {
		result.Temperature = def.Temperature
	}
	if result.MaxTokens <= 0 {
		result.MaxTokens = def.MaxTokens
	}
	savedByID := map[string]PromptField{}
	for _, f := range saved.Fields {
		savedByID[f.Key] = f
	}
	for _, df := range def.Fields {
		if sv, ok := savedByID[df.Key]; ok {
			f := df
			f.Label = sv.Label
			f.Icon = sv.Icon
			f.Desc = sv.Desc
			f.Enabled = sv.Enabled
			if df.Key == "word" {
				f.Enabled = true
			}
			result.Fields = append(result.Fields, f)
			delete(savedByID, df.Key)
		} else {
			result.Fields = append(result.Fields, df)
		}
	}
	for _, f := range saved.Fields {
		if _, ok := savedByID[f.Key]; ok {
			f.Builtin = false
			result.Fields = append(result.Fields, f)
		}
	}
	return result
}

// ============================================================
// DictService - ECDICT fast lookup + LLM enrichment
// ============================================================

type DictService struct {
	app          *application.App
	apiKey       string
	apiURL       string
	modelName    string
	shortcutKey  string
	autoStart    bool
	ready        bool
	ecdict       *EcdictService
	promptConfig *PromptConfig
	httpClient   *http.Client       // shared client with connection pooling
	history      *HistoryService    // for cache lookup
	resultCache  *lruCache         // LRU cache: word -> merged JSON result (bounded size)
}

func (d *DictService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	d.app = application.Get()

	// Shared HTTP client with connection pooling for LLM calls
	d.httpClient = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        2,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}
	d.resultCache = newLRUCache(100) // max 100 entries (~200-500KB total)
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
			if v, ok := cfg["autoStart"]; ok {
				d.autoStart, _ = strconv.ParseBool(v)
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

	// Sync auto-start state with registry (in case registry was manually changed)
	if d.autoStart && !checkAutoStartRegistry() {
		log.Println("Auto-start was enabled in config but missing from registry, re-enabling...")
		if err := enableAutoStart(); err != nil {
			log.Printf("Failed to re-enable auto-start: %v", err)
			d.autoStart = false
		}
	} else if !d.autoStart && checkAutoStartRegistry() {
		log.Println("Auto-start was disabled in config but still in registry, removing...")
		disableAutoStart()
	}

	// Load prompt configuration (falls back to defaults if missing)
	promptPath := getConfigPath("prompt_config.json")
	if pdata, perr := os.ReadFile(promptPath); perr == nil {
		var savedCfg PromptConfig
		if json.Unmarshal(pdata, &savedCfg) == nil {
			d.promptConfig = mergePromptConfig(&savedCfg)
		} else {
			d.promptConfig = defaultPromptConfig()
		}
	} else {
		d.promptConfig = defaultPromptConfig()
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
	start := time.Now()
	entry := d.ecdict.LookupEcdict(word)
	elapsed := time.Since(start)
	log.Printf("[ECDICT-DEBUG] LookupEcdict(%q) took %v, found=%v", word, elapsed, entry != nil)
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

// LookupWordCached checks the in-memory cache and history DB for a previously
// looked-up word. Returns the full merged JSON result (ECDICT+LLM) if found,
// or empty string if not cached. This avoids redundant LLM calls for words
// the user has already looked up.
func (d *DictService) LookupWordCached(word string) string {
	word = strings.TrimSpace(strings.ToLower(word))
	if word == "" {
		return ""
	}

	// 1. Check in-memory LRU cache (fastest)
	if cached, ok := d.resultCache.get(word); ok {
		log.Printf("[LLM-DEBUG] Cache HIT (memory) for %q", word)
		return cached
	}

	// 2. Check history DB (persistent cache)
	if d.history != nil {
		entry := d.history.GetHistoryByWord(word)
		if entry != nil && entry.Result != "" {
			// Populate in-memory cache for next time
			d.resultCache.set(word, entry.Result)
			log.Printf("[LLM-DEBUG] Cache HIT (history DB) for %q", word)
			return entry.Result
		}
	}

	log.Printf("[LLM-DEBUG] Cache MISS for %q", word)
	return ""
}

// CacheResult stores a merged lookup result in the in-memory cache.
// Call this after a successful full lookup so subsequent lookups of the same
// word are served from cache instantly.
func (d *DictService) CacheResult(word, result string) {
	word = strings.TrimSpace(strings.ToLower(word))
	if word == "" || result == "" {
		return
	}
	d.resultCache.set(word, result)
}

// LookupWordLLM calls LLM to get enriched word definition.
// This is the slow path (~2-10s), called after ECDICT fast result is shown.
func (d *DictService) LookupWordLLM(word string) (string, error) {
	return d.lookupWordLLMInternal(word, true)
}

// LookupWordLLMFast calls LLM without re-querying ECDICT for the prompt context.
// Use this when the frontend already has the ECDICT data from LookupWordFast.
func (d *DictService) LookupWordLLMFast(word string) (string, error) {
	return d.lookupWordLLMInternal(word, false)
}

func (d *DictService) lookupWordLLMInternal(word string, includeEcdict bool) (string, error) {
	log.Printf("[LLM-DEBUG] lookupWordLLMInternal called: word=%q includeEcdict=%v", word, includeEcdict)
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

	cfg := d.promptConfig
	if cfg == nil {
		cfg = defaultPromptConfig()
	}
	prompt := d.buildUserPrompt(cfg, word, includeEcdict)
	return d.callLLM(cfg.SystemPrompt, prompt, cfg.Temperature, cfg.MaxTokens)
}

// callLLM sends a chat-completion request and returns the cleaned JSON content.
func (d *DictService) callLLM(system, user string, temperature float64, maxTokens int) (string, error) {
	startTime := time.Now()

	// Build messages with cache_control on the system message for prompt caching.
	// OpenAI: automatically caches prompts >= 1024 tokens; cache_control is a hint.
	// Anthropic/compatible: uses cache_control breakpoints.
	// The system prompt is static across requests, so it's the best candidate for caching.
	reqBody := map[string]interface{}{
		"model": d.modelName,
		"messages": []map[string]interface{}{
			{
				"role":    "system",
				"content": system,
			},
			{
				"role":    "user",
				"content": user,
			},
		},
		"temperature": temperature,
		"max_tokens":  maxTokens,
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

	log.Printf("[LLM-DEBUG] === New Request ===")
	log.Printf("[LLM-DEBUG] URL: %s", apiURL)
	log.Printf("[LLM-DEBUG] Model: %s", d.modelName)
	log.Printf("[LLM-DEBUG] Temperature: %.2f, MaxTokens: %d", temperature, maxTokens)
	log.Printf("[LLM-DEBUG] System prompt (%d chars): %s", len(system), truncate(system, 100))
	log.Printf("[LLM-DEBUG] User prompt (%d chars): %s", len(user), truncate(user, 150))
	log.Printf("[LLM-DEBUG] Request body size: %d bytes", len(jsonData))
	// Log API key (masked) for debugging
	apiKeyPreview := "???"
	if len(d.apiKey) > 6 {
		apiKeyPreview = d.apiKey[:3] + "..." + d.apiKey[len(d.apiKey)-3:]
	}
	log.Printf("[LLM-DEBUG] API Key: %s (len=%d)", apiKeyPreview, len(d.apiKey))

	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)

	log.Printf("[LLM-DEBUG] Sending request...")
	resp, err := d.httpClient.Do(req)
	connectDuration := time.Since(startTime)
	if err != nil {
		log.Printf("[LLM-DEBUG] Request FAILED after %v: %v", connectDuration, err)
		if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
			log.Printf("[LLM-DEBUG] Error was a TIMEOUT")
		}
		return "", fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	log.Printf("[LLM-DEBUG] Response status: %d (received headers in %v)", resp.StatusCode, connectDuration)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %v", err)
	}

	totalDuration := time.Since(startTime)
	log.Printf("[LLM-DEBUG] Response body size: %d bytes (total time: %v)", len(body), totalDuration)

	if resp.StatusCode != 200 {
		log.Printf("[LLM-DEBUG] API error: status=%d url=%s body=%s", resp.StatusCode, apiURL, truncate(string(body), 500))
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
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			// OpenAI prompt cache fields
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		// Some providers nest cache info differently
		CachedTokens int `json:"cached_tokens"`
	} // top-level response struct
	if err := json.Unmarshal(body, &chatResp); err != nil {
		log.Printf("[LLM-DEBUG] Failed to parse response JSON: %v, raw body: %s", err, truncate(string(body), 300))
		return "", fmt.Errorf("解析响应失败: %v", err)
	}
	if len(chatResp.Choices) == 0 {
		log.Printf("[LLM-DEBUG] No choices in response, raw body: %s", truncate(string(body), 300))
		return "", fmt.Errorf("API未返回有效内容")
	}

	log.Printf("[LLM-DEBUG] Finish reason: %s", chatResp.Choices[0].FinishReason)
	log.Printf("[LLM-DEBUG] Token usage - prompt: %d, completion: %d, total: %d",
		chatResp.Usage.PromptTokens, chatResp.Usage.CompletionTokens, chatResp.Usage.TotalTokens)

	// Log prompt cache metrics
	cachedTokens := chatResp.Usage.PromptTokensDetails.CachedTokens
	if cachedTokens == 0 {
		cachedTokens = chatResp.CachedTokens // fallback for some providers
	}
	if cachedTokens > 0 {
		cacheHitRate := float64(cachedTokens) / float64(chatResp.Usage.PromptTokens) * 100
		savedTokens := cachedTokens // cached tokens are billed at 50% (OpenAI) or free (Anthropic)
		log.Printf("[LLM-DEBUG] Prompt cache HIT: %d/%d tokens cached (%.1f%% hit rate, ~%d tokens saved)",
			cachedTokens, chatResp.Usage.PromptTokens, cacheHitRate, savedTokens)
	} else {
		log.Printf("[LLM-DEBUG] Prompt cache MISS: 0 cached tokens out of %d prompt tokens", chatResp.Usage.PromptTokens)
	}

	content := chatResp.Choices[0].Message.Content
	log.Printf("[LLM-DEBUG] Raw content length: %d chars", len(content))
	log.Printf("[LLM-DEBUG] Raw content preview: %s", truncate(content, 200))

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

	log.Printf("[LLM-DEBUG] Cleaned content length: %d chars", len(content))
	log.Printf("[LLM-DEBUG] === Request complete in %v ===", totalDuration)

	return content, nil
}

// buildEcdictInfo returns the ECDICT context fragment appended to the user prompt
// when the word exists in the offline dictionary.
func (d *DictService) buildEcdictInfo(word string) string {
	if d.ecdict == nil {
		return ""
	}
	entry := d.ecdict.LookupEcdict(word)
	if entry == nil {
		return ""
	}
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
	if len(parts) == 0 {
		return ""
	}
	return "\n\n已知基础信息（ECDICT离线词典）：\n" + strings.Join(parts, "\n") + "\n\n请在以上基础上补充更丰富的内容，避免重复已有信息。"
}

// buildUserPrompt assembles the user message from the prompt config and word.
// The prompt is structured for maximum LLM prompt cache hit rate:
//   [STATIC PREFIX] schema + instructions + extra requirements
//   [VARIABLE SUFFIX] word + ECDICT context
// This way, the static prefix is identical across requests and can be cached
// by the LLM provider (OpenAI/Anthropic prompt caching), while only the
// variable suffix changes per word.
func (d *DictService) buildUserPrompt(cfg *PromptConfig, word string, includeEcdict bool) string {
	// ── Static prefix: schema + instructions (same for every word) ──
	var lines []string
	for _, f := range cfg.Fields {
		if !f.Enabled {
			continue
		}
		if f.Key == "word" {
			// Placeholder — the actual word goes in the variable suffix
			lines = append(lines, `  "word": "..."`)
		} else if f.Key == "definitions" {
			lines = append(lines, definitionsSchemaBlock)
		} else {
			lines = append(lines, fmt.Sprintf(`  "%s": "%s"`, f.Key, f.Desc))
		}
	}
	schema := "{\n" + strings.Join(lines, ",\n") + "\n}"
	closing := "\n请确保返回纯JSON，不要有其他内容。"
	if cfg.fieldEnabled("definitions") {
		closing += "如果有多个词性，请在definitions中分别列出。"
	}
	extra := ""
	if strings.TrimSpace(cfg.ExtraRequirements) != "" {
		extra = "\n\n额外要求：" + cfg.ExtraRequirements
	}

	staticPrefix := fmt.Sprintf("请对英语单词或短语进行详细解释，严格按照以下JSON格式返回（不要包含markdown代码块标记）：\n\n%s\n%s%s", schema, closing, extra)

	// ── Variable suffix: word + ECDICT context (changes per word) ──
	variableSuffix := fmt.Sprintf("\n\n---\n查询单词：%s", word)
	if includeEcdict {
		ecdictInfo := d.buildEcdictInfo(word)
		if ecdictInfo != "" {
			variableSuffix += ecdictInfo
		}
	}

	return staticPrefix + variableSuffix
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
		"autoStart":   strconv.FormatBool(d.autoStart),
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
		"autoStart":   strconv.FormatBool(d.autoStart),
	}
}

// SetAutoStart enables or disables auto-start on system boot.
// On Windows it writes/removes a Run registry key under HKCU.
func (d *DictService) SetAutoStart(enable bool) error {
	d.autoStart = enable

	// Persist to config.json
	configPath := getConfigPath("config.json")
	data, _ := json.MarshalIndent(map[string]string{
		"apiKey":      d.apiKey,
		"apiURL":      d.apiURL,
		"modelName":   d.modelName,
		"shortcutKey": d.shortcutKey,
		"autoStart":   strconv.FormatBool(enable),
	}, "", "  ")
	os.WriteFile(configPath, data, 0644)

	if enable {
		return enableAutoStart()
	}
	return disableAutoStart()
}

// GetAutoStart returns whether auto-start is currently enabled.
func (d *DictService) GetAutoStart() bool {
	return d.autoStart
}

// enableAutoStart adds the app to HKCU\...\Run registry key.
func enableAutoStart() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get exe path: %w", err)
	}
	// Resolve symlinks
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve exe path: %w", err)
	}

	// Use reg.exe to add the registry entry (no CGo needed)
	cmd := exec.Command("reg", "add",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run",
		"/v", "WordWise",
		"/t", "REG_SZ",
		"/d", `"`+exePath+`"`,
		"/f",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("reg add failed: %w, output: %s", err, string(output))
	}
	log.Printf("Auto-start enabled: %s", exePath)
	return nil
}

// disableAutoStart removes the app from HKCU\...\Run registry key.
func disableAutoStart() error {
	cmd := exec.Command("reg", "delete",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run",
		"/v", "WordWise",
		"/f",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("reg delete failed: %w, output: %s", err, string(output))
	}
	log.Println("Auto-start disabled")
	return nil
}

// checkAutoStartRegistry checks if the Run key exists for WordWise.
func checkAutoStartRegistry() bool {
	cmd := exec.Command("reg", "query",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run",
		"/v", "WordWise",
	)
	err := cmd.Run()
	return err == nil
}

// GetCacheStats returns cache statistics for debugging.
func (d *DictService) GetCacheStats() map[string]interface{} {
	cacheSize := d.resultCache.len()

	stats := map[string]interface{}{
		"memoryCacheSize": cacheSize,
		"model":           d.modelName,
		"apiURL":          d.apiURL,
	}

	// Build a preview of the prompt structure for cache analysis
	cfg := d.promptConfig
	if cfg == nil {
		cfg = defaultPromptConfig()
	}

	// Show the static prefix (cacheable part)
	var lines []string
	for _, f := range cfg.Fields {
		if !f.Enabled {
			continue
		}
		if f.Key == "word" {
			lines = append(lines, `  "word": "..."`)
		} else if f.Key == "definitions" {
			lines = append(lines, definitionsSchemaBlock)
		} else {
			lines = append(lines, fmt.Sprintf(`  "%s": "%s"`, f.Key, f.Desc))
		}
	}
	schema := "{\n" + strings.Join(lines, ",\n") + "\n}"
	closing := "\n请确保返回纯JSON，不要有其他内容。"
	if cfg.fieldEnabled("definitions") {
		closing += "如果有多个词性，请在definitions中分别列出。"
	}
	extra := ""
	if strings.TrimSpace(cfg.ExtraRequirements) != "" {
		extra = "\n\n额外要求：" + cfg.ExtraRequirements
	}
	staticPrefix := fmt.Sprintf("请对英语单词或短语进行详细解释，严格按照以下JSON格式返回（不要包含markdown代码块标记）：\n\n%s\n%s%s", schema, closing, extra)

	stats["systemPromptLength"] = len(cfg.SystemPrompt)
	stats["staticPrefixLength"] = len(staticPrefix)
	stats["staticPrefixPreview"] = truncate(staticPrefix, 300)
	stats["variableSuffixNote"] = "word + ECDICT context (changes per word, appended after static prefix)"
	stats["cacheStrategy"] = "Static prefix (system prompt + schema + instructions) is identical across requests → LLM provider can cache it. Variable suffix (word + ECDICT) changes per request."

	return stats
}

// GetPromptConfig returns the current prompt configuration as a JSON string.
func (d *DictService) GetPromptConfig() string {
	cfg := d.promptConfig
	if cfg == nil {
		cfg = defaultPromptConfig()
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

// SavePromptConfig saves the prompt configuration (provided as a JSON string).
func (d *DictService) SavePromptConfig(configJSON string) error {
	var cfg PromptConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("提示词配置格式错误: %v", err)
	}
	merged := mergePromptConfig(&cfg)
	d.promptConfig = merged
	configPath := getConfigPath("prompt_config.json")
	data, _ := json.MarshalIndent(merged, "", "  ")
	return os.WriteFile(configPath, data, 0644)
}

// TestPrompt builds a prompt from the given config JSON and calls the LLM for a test word.
// Returns the raw LLM JSON response (same cleaning as LookupWordLLM).
func (d *DictService) TestPrompt(word string, configJSON string) (string, error) {
	if !d.ready {
		return "", fmt.Errorf("服务正在初始化，请稍后重试")
	}
	if d.apiKey == "" {
		return "", fmt.Errorf("请先设置 API Key")
	}
	word = strings.TrimSpace(word)
	if word == "" {
		return "", fmt.Errorf("请输入测试单词")
	}
	var cfg PromptConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return "", fmt.Errorf("提示词配置格式错误: %v", err)
	}
	merged := mergePromptConfig(&cfg)
	prompt := d.buildUserPrompt(merged, word, true)
	return d.callLLM(merged.SystemPrompt, prompt, merged.Temperature, merged.MaxTokens)
}

// GetPromptDebugInfo returns the exact system and user prompts that would be sent
// for a given word, without actually calling the LLM. Useful for debugging
// prompt cache behavior and prompt structure.
func (d *DictService) GetPromptDebugInfo(word string) map[string]string {
	word = strings.TrimSpace(word)
	if word == "" {
		return map[string]string{"error": "word is empty"}
	}

	cfg := d.promptConfig
	if cfg == nil {
		cfg = defaultPromptConfig()
	}

	systemPrompt := cfg.SystemPrompt
	userPrompt := d.buildUserPrompt(cfg, word, true)

	// Find the split point between static prefix and variable suffix
	splitMarker := "\n\n---\n查询单词："
	splitIdx := strings.Index(userPrompt, splitMarker)

	staticPart := userPrompt
	variablePart := ""
	if splitIdx >= 0 {
		staticPart = userPrompt[:splitIdx]
		variablePart = userPrompt[splitIdx:]
	}

	return map[string]string{
		"systemPrompt":       systemPrompt,
		"userPrompt":         userPrompt,
		"staticPrefix":       staticPart,
		"variableSuffix":     variablePart,
		"systemPromptLen":    fmt.Sprintf("%d", len(systemPrompt)),
		"staticPrefixLen":    fmt.Sprintf("%d", len(staticPart)),
		"variableSuffixLen":  fmt.Sprintf("%d", len(variablePart)),
		"totalPromptLen":     fmt.Sprintf("%d", len(systemPrompt)+len(userPrompt)),
		"cacheNote":          "The static prefix (system + schema + instructions) is identical across requests and can be cached by the LLM provider. The variable suffix (word + ECDICT) changes per request.",
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
				if w.IsVisible() {
					// Window is visible — hide it (toggle off)
					log.Printf("[CLIPBOARD-DEBUG] Window visible, hiding")
					w.Hide()
					return
				}
				// Window is hidden — show it and read clipboard
				w.Show()
				w.Focus()
			}
			text, ok := d.app.Clipboard.Text()
			log.Printf("[CLIPBOARD-DEBUG] Shortcut pressed, clipboard: ok=%v text=%q", ok, truncate(text, 50))
			if ok && isEnglishText(text) {
				word := strings.TrimSpace(text)
				log.Printf("[CLIPBOARD-DEBUG] Emitting clipboard-english-detected: %q", word)
				d.app.Event.Emit("clipboard-english-detected", word)
			} else {
				log.Printf("[CLIPBOARD-DEBUG] Clipboard text not English or read failed")
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

// AddHistory adds a new entry or updates an existing one (same word).
// If skipSync is true, the sync callback is NOT triggered (used when importing from server).
func (h *HistoryService) AddHistory(word, result string) error {
	return h.addHistoryInternal(word, result, false)
}

// AddHistoryFromSync adds/updates an entry from a server pull, without triggering
// the sync callback (to avoid pushing it right back to the server).
func (h *HistoryService) AddHistoryFromSync(word, result string) error {
	return h.addHistoryInternal(word, result, true)
}

func (h *HistoryService) addHistoryInternal(word, result string, skipSync bool) error {
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

	// Notify sync service (non-blocking) — skip if importing from server
	if !skipSync && h.syncCb != nil {
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

// GetHistoryByWord returns a single entry by word (case-insensitive).
// Used for cache lookup to avoid redundant LLM calls.
func (h *HistoryService) GetHistoryByWord(word string) *HistoryEntry {
	h.mu.RLock()
	db := h.db
	h.mu.RUnlock()

	if db == nil {
		return nil
	}

	var e HistoryEntry
	err := db.QueryRow(
		"SELECT id, word, result, created_at, updated_at FROM history WHERE word = ? COLLATE NOCASE",
		word,
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
	history       *HistoryService
	syncAddr      string // 远程同步服务器地址，如 http://your-server:9274
	syncToken     string // 用户Token（由服务器分配）
	autoSync      bool   // 是否启用自动同步
	lastSyncTime  int64  // Unix timestamp of last successful sync (for incremental pull)
}

func (s *SyncService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.loadConfig()
	// Auto-pull on startup (background, non-blocking, incremental)
	if s.autoSync && s.syncAddr != "" && s.syncToken != "" {
		go func() {
			msg, err := s.pullFromServerInternal(true)
			if err != nil {
				log.Printf("SyncService: startup auto-pull failed: %v", err)
			} else if msg != "" {
				log.Printf("SyncService: startup auto-pull: %s", msg)
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
		SyncAddr     string `json:"syncAddr"`
		SyncToken    string `json:"syncToken"`
		AutoSync     bool   `json:"autoSync"`
		LastSyncTime int64  `json:"lastSyncTime"`
	}
	if json.Unmarshal(data, &cfg) == nil {
		s.syncAddr = cfg.SyncAddr
		s.syncToken = cfg.SyncToken
		s.autoSync = cfg.AutoSync
		s.lastSyncTime = cfg.LastSyncTime
	}
}

func (s *SyncService) saveConfig() error {
	configPath := getConfigPath("sync_config.json")
	data, _ := json.MarshalIndent(map[string]interface{}{
		"syncAddr":     s.syncAddr,
		"syncToken":    s.syncToken,
		"autoSync":     s.autoSync,
		"lastSyncTime": s.lastSyncTime,
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
// Uses incremental sync: only pulls entries updated since the last successful sync.
// Falls back to full pull if never synced before.
func (s *SyncService) PullFromServer() (string, error) {
	return s.pullFromServerInternal(false)
}

func (s *SyncService) pullFromServerInternal(silent bool) (string, error) {
	if s.syncAddr == "" || s.syncToken == "" {
		return "", fmt.Errorf("请先配置同步服务器地址和Token")
	}
	if s.history == nil {
		return "", fmt.Errorf("历史服务未初始化")
	}

	// Build URL with ?since= for incremental sync
	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/sync/pull"
	since := s.lastSyncTime
	if since > 0 {
		url += fmt.Sprintf("?since=%d", since)
		log.Printf("SyncService: incremental pull since %d", since)
	} else {
		log.Printf("SyncService: full pull (first sync)")
	}

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
		// No new data, but still update lastSyncTime to avoid re-fetching
		if result.ServerNow > 0 {
			s.lastSyncTime = result.ServerNow
			s.saveConfig()
		}
		return "服务器上没有新数据", nil
	}

	// Merge into local history (using AddHistoryFromSync to avoid push-back loop)
	merged := 0
	for _, e := range result.Entries {
		if e.Deleted {
			s.history.DeleteHistory(e.ID)
			continue
		}
		s.history.AddHistoryFromSync(e.Word, e.Result)
		merged++
	}

	// Save lastSyncTime from server response for incremental sync
	if result.ServerNow > 0 {
		s.lastSyncTime = result.ServerNow
		s.saveConfig()
	}

	log.Printf("SyncService: pulled %d entries (since=%d, serverNow=%d)", merged, since, result.ServerNow)

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
