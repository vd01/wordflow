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
	"net/url"
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

	"wordflow/syncserver"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

// setupFileLogging redirects log output to a file in the WordWise config dir
// (in addition to stderr), so debug timings survive GUI launch.
func setupFileLogging() {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	dir = filepath.Join(dir, "WordWise")
	os.MkdirAll(dir, 0755)
	f, err := os.OpenFile(filepath.Join(dir, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.Println("===== WordFlow started, file logging enabled =====")
}

func main() {
	setupFileLogging()
	ecdictSvc := &EcdictService{}
	historySvc := &HistoryService{}
	syncSvc := &SyncService{history: historySvc}

	// Wire up: HistoryService notifies SyncService on new entries
	historySvc.syncCb = syncSvc.OnEntryAdded
	historySvc.syncBulkCb = syncSvc.OnEntriesDeleted

	app := application.New(application.Options{
		Name:        "WordFlow",
		Description: "查词温故 - 系统托盘 + 全局快捷键 + ECDICT离线词典 + LLM智能查词 + 多设备同步",
		Windows: application.WindowsOptions{
			AdditionalBrowserArgs: []string{"--remote-debugging-port=9222", "--remote-allow-origins=*"},
		},
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
			UniqueID: "com.wordflow.app",
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
	tray.SetTooltip("WordFlow - 查词温故 (Ctrl+Alt+Q 呼出)")

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
		Title:            "WordFlow - 查词温故",
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
// proxyFallbackTransport - HTTP proxy with automatic direct fallback
// ============================================================

// proxyFallbackTransport routes requests through the proxy transport first.
// If the proxy is unreachable (e.g. Clash not running), it retries once with
// the direct transport so the app keeps working without the proxy.
// Retrying is safe here: Go's http.Transport only returns an error (instead of
// a response) for connection-level failures, i.e. before the request body was
// fully transmitted.
type proxyFallbackTransport struct {
	proxy  http.RoundTripper
	direct http.RoundTripper
}

func (t *proxyFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.proxy.RoundTrip(req)
	if err == nil && !isProxyFailure(resp.StatusCode) {
		return resp, nil
	}
	// Proxy failed (connection error, or proxy-level 502/503/504 e.g. Clash
	// has no route for the target) — retry once directly. Drain the proxy
	// response so its connection can be reused.
	if resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	// Clone the request and rewind the body (http.NewRequest with bytes.Reader
	// sets GetBody, so this works for all our POST bodies). If the body can't
	// be replayed, just return the proxy error instead of risking a corrupt retry.
	clone := req.Clone(req.Context())
	if req.Body != nil {
		if req.GetBody == nil {
			if err != nil {
				return nil, err
			}
			return resp, nil
		}
		body, berr := req.GetBody()
		if berr != nil {
			if err != nil {
				return nil, err
			}
			return resp, nil
		}
		clone.Body = body
	}
	resp2, err2 := t.direct.RoundTrip(clone)
	if err2 != nil {
		// Both failed — report the proxy error (more informative).
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
	return resp2, nil
}

// isProxyFailure reports whether the status code indicates a proxy-level
// failure (as opposed to a real error from the upstream server).
func isProxyFailure(status int) bool {
	switch status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
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
	CorrectedFrom string `json:"corrected_from"` // original word when corrected via fuzzy match
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
	// DB is ~101MB (770K rows, 25K pages). Cache/mmap must cover most of it.
	pragmas := []string{
		"PRAGMA journal_mode=OFF",     // No WAL overhead for read-only
		"PRAGMA synchronous=OFF",      // No fsync on reads
		"PRAGMA cache_size=-65536",     // 64MB page cache (covers ~63% of DB)
		"PRAGMA mmap_size=134217728",   // 128MB memory-mapped I/O (covers full DB)
		"PRAGMA busy_timeout=5000",     // Wait up to 5s for lock
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
// then falls back to title-case lookup, and finally tries fuzzy (Levenshtein) matching
// for minor spelling mistakes (transpositions, single char off).
//
// Performance notes (measured with modernc.org/sqlite on 770K-row ECDICT):
//   - Exact match (PRIMARY KEY index):  ~0.07ms  ← fast path
//   - COLLATE NOCASE (full table scan):  ~30-80ms ← SLOW, avoided when possible
//   - Fuzzy prefix + Levenshtein:        ~0.3ms   ← acceptable
//
// The caller (LookupWordFast) already lowercases the input, so step 2 (lowercase
// retry) is almost always redundant. Step 3 (COLLATE NOCASE) was replaced by a
// targeted title-case lookup which uses the index instead of a full table scan.
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

	// 1) Try exact match first (uses PRIMARY KEY index — fastest, ~0.07ms)
	entry := e.queryWord(db, word)
	if entry != nil {
		return entry
	}

	// 2) Try lowercase match (covers most case mismatches, still index-friendly)
	lowerWord := strings.ToLower(word)
	if lowerWord != word {
		entry = e.queryWord(db, lowerWord)
		if entry != nil {
			return entry
		}
	}

	// 3) Try title-case match (index-friendly alternative to COLLATE NOCASE)
	//    ECDICT stores ~81K proper nouns in title-case (e.g. "Bahai", "Sabbatarian").
	//    When the user types the lowercase form, exact + lowercase both miss.
	//    Previously we used COLLATE NOCASE which does a FULL TABLE SCAN (~30-80ms!).
	//    Instead, capitalize the first letter and try an indexed lookup (~0.07ms).
	if len(lowerWord) > 0 && lowerWord[0] >= 'a' && lowerWord[0] <= 'z' {
		titleWord := strings.ToUpper(string(lowerWord[0])) + lowerWord[1:]
		if titleWord != word && titleWord != lowerWord {
			entry = e.queryWord(db, titleWord)
			if entry != nil {
				return entry
			}
		}
	}

	// 3b) COLLATE NOCASE — ONLY for multi-word phrases (rare edge case).
	//    ECDICT has ~81K entries with unusual casing ("A AND NOT B gate", "iPhone").
	//    For single words the title-case lookup above already covers the common
	//    case without a full scan. Phrases are rare enough that the occasional
	//    30-80ms full scan is acceptable.
	if strings.Contains(word, " ") || strings.Contains(word, "-") {
		entry = e.queryWordCollate(db, word)
		if entry != nil {
			return entry
		}
	}

	// 4) Fuzzy match: find the closest word for minor spelling mistakes
	//    This covers transpositions ("recieve"→"receive"), single wrong/missing/extra chars
	entry = e.lookupFuzzy(db, word)
	if entry != nil {
		// Mark the original word so the frontend can show "Did you mean X?"
		entry.CorrectedFrom = word
		log.Printf("[ECDICT-DEBUG] Fuzzy match for %q: found %q", word, entry.Word)
		return entry
	}

	return nil
}

func (e *EcdictService) queryWord(db *sql.DB, word string) *EcdictEntry {
	row := db.QueryRow(
		"SELECT word, phonetic, definition, translation, pos, collins, oxford, tag, bnc, frq, exchange FROM ecdict WHERE word = ?",
		word,
	)
	return e.scanEntry(row)
}

// queryWordCollate is DEPRECATED — kept only for reference.
// COLLATE NOCASE forces a full table scan (~30-80ms on 770K rows).
// Replaced by title-case lookup in step 3 of LookupEcdict.
func (e *EcdictService) queryWordCollate(db *sql.DB, word string) *EcdictEntry {
	row := db.QueryRow(
		"SELECT word, phonetic, definition, translation, pos, collins, oxford, tag, bnc, frq, exchange FROM ecdict WHERE word = ? COLLATE NOCASE",
		word,
	)
	return e.scanEntry(row)
}

// lookupFuzzy searches for words that are similar to the query using
// same-first-letter prefix matching + Levenshtein distance.
// Returns the closest match if edit distance ≤ 2, otherwise nil.
func (e *EcdictService) lookupFuzzy(db *sql.DB, word string) *EcdictEntry {
	if len(word) < 2 {
		return nil
	}

	// Get candidates with the same first letter to narrow the search
	// Use SQL prefix range for fast index scan (all- lowercase for correct byte ordering)
	firstChar := strings.ToLower(string(word[0]))
	rows, err := db.Query(
		"SELECT word FROM ecdict WHERE word >= ? AND word < ? LIMIT 500",
		firstChar,
		string(firstChar[0]+1),
	)
	if err != nil {
		log.Printf("[ECDICT-DEBUG] Fuzzy prefix query error: %v", err)
		return nil
	}
	defer rows.Close()

	type candidate struct {
		word  string
		dist  int
	}
	var best candidate
	best.dist = 3 // threshold: only accept distance ≤ 2
	lowerQuery := strings.ToLower(word)

	for rows.Next() {
		var candidateWord string
		if err := rows.Scan(&candidateWord); err != nil {
			continue
		}
		// Quick length filter: skip if length differs by more than 2
		if abs(len(candidateWord)-len(word)) > 2 {
			continue
		}
		dist := levenshteinDistance(lowerQuery, strings.ToLower(candidateWord))
		if dist < best.dist {
			best.word = candidateWord
			best.dist = dist
		}
	}

	if best.dist <= 2 && best.word != "" {
		return e.queryWord(db, best.word)
	}
	return nil
}

// levenshteinDistance computes the edit distance between two strings.
// Uses the standard two-row DP approach for O(n*m) time and O(n) space.
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	// Make a the shorter string for smaller memory usage
	if len(a) > len(b) {
		a, b = b, a
	}
	la, lb := len(a), len(b)
	// Single allocation for the DP row
	row := make([]int, la+1)
	for i := 1; i <= la; i++ {
		row[i] = i
	}
	for j := 1; j <= lb; j++ {
		prev := row[0]
		row[0] = j
		for i := 1; i <= la; i++ {
			temp := row[i]
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			row[i] = min3(temp+1, row[i-1]+1, prev+cost)
			prev = temp
		}
	}
	return row[la]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
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

	// Note: idx_ecdict_word is REDUNDANT — word is already the PRIMARY KEY,
	// so sqlite_autoindex_ecdict_1 covers exact-match lookups.
	// The redundant index wastes ~50MB disk space and slows import.
	// Kept for now because the fuzzy prefix query benefits from the covering index
	// (SELECT word FROM ecdict WHERE word >= ? AND word < ?), which avoids
	// hitting the main table. A future optimization could use a separate
	// covering index or restructure the fuzzy query.
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
		SystemPrompt:      "你是一个专业的查词温故，总是以纯JSON格式回复，不包含markdown标记。用户会给你一个英语单词或短语，你必须解释它。",
		ExtraRequirements: "",
		Temperature:       0.3,
		MaxTokens:         2000,
		Fields: []PromptField{
			{Key: "word", Label: "单词", Icon: "🔤", Type: "string", Desc: "被查询的英语单词或短语", Enabled: true, Builtin: true},
			{Key: "phonetic", Label: "音标", Icon: "🎵", Type: "string", Desc: "音标（国际音标）", Enabled: true, Builtin: true},
			{Key: "pronunciation", Label: "发音提示", Icon: "🗣️", Type: "string", Desc: "发音提示（用中文近似标注）", Enabled: false, Builtin: true},
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

	// Force-disable Chinese homophone pronunciation mnemonics for all users
	for i := range result.Fields {
		if result.Fields[i].Key == "pronunciation" {
			result.Fields[i].Enabled = false
			break
		}
	}
	return result
}

// ============================================================
// DictService - ECDICT fast lookup + LLM enrichment
// ============================================================

// ErrLLMNotConfigured is returned when LLM lookup is attempted but
// apiKey, apiURL, or modelName is not set. The frontend uses this
// to skip Phase 2 gracefully and show a setup hint instead of an error.
var ErrLLMNotConfigured = fmt.Errorf("LLM not configured: please set API Key, API URL, and Model Name in settings")

type DictService struct {
	app          *application.App
	apiKey       string
	apiURL       string
	modelName    string
	shortcutKey  string
	autoStart    bool
	proxy        string // HTTP proxy for LLM/audio calls (e.g. http://127.0.0.1:7993)
	ready        bool
	ecdict       *EcdictService
	promptConfig *PromptConfig
	httpClient   *http.Client       // shared client with connection pooling
	history      *HistoryService    // for cache lookup
	resultCache  *lruCache         // LRU cache: word -> merged JSON result (bounded size)

	// pendingWord is set by the global hotkey and pulled by the frontend.
	// WebView2 throttles/freezes the renderer while the window is hidden, so
	// event delivery to the frontend can be delayed or lost; polling this
	// field guarantees the hotkey word is picked up as soon as the frontend
	// is responsive again.
	pendingWord string
	pendingMu    sync.Mutex
}

func (d *DictService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	d.app = application.Get()

	// ── Load config FIRST (proxy is needed when building the transport below) ──
	configPath := getConfigPath("config.json")
	data, err := os.ReadFile(configPath)
	hasProxy := false
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
			if v, ok := cfg["proxy"]; ok {
				d.proxy = v // empty = direct connection
				hasProxy = true
			}
		}
	}
	// Default proxy: Clash runs on 127.0.0.1:7993 (applies only when the config
	// key is absent; an explicit empty string in config disables the proxy).
	if !hasProxy {
		d.proxy = "http://127.0.0.1:7993"
	}

	// ── Shared HTTP client with connection pooling for LLM calls ──
	// Measured on DeepSeek: generation runs ~85 tok/s, so a 2000-token
	// max_tokens response can legitimately take ~24s. The 30s total timeout
	// previously caused hard failures on slow (but valid) responses → bump to 60s.
	// IdleConnTimeout was 30s, which killed the keep-alive between sporadic
	// lookups, forcing a fresh DNS+TCP+TLS handshake (~65-185ms) on every
	// lookup after a 30s pause → bump to 2min and allow more idle conns.
	//
	// Proxy: requests go through the proxy first (e.g. Clash on 127.0.0.1:7993)
	// and automatically fall back to a direct connection when the proxy is
	// unreachable — so the app never breaks when Clash is off.
	directTransport := &http.Transport{
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     2 * time.Minute,
	}
	var transport http.RoundTripper = directTransport
	if d.proxy != "" {
		if proxyURL, perr := url.Parse(d.proxy); perr == nil && proxyURL.Scheme != "" {
			proxyTransport := &http.Transport{
				Proxy:               http.ProxyURL(proxyURL),
				MaxIdleConns:        8,
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     2 * time.Minute,
			}
			transport = &proxyFallbackTransport{proxy: proxyTransport, direct: directTransport}
			log.Printf("DictService: HTTP proxy enabled: %s (fallback to direct if unreachable)", d.proxy)
		} else {
			log.Printf("DictService: invalid proxy URL %q, using direct connection", d.proxy)
		}
	}
	d.httpClient = &http.Client{
		Timeout:   60 * time.Second,
		Transport: transport,
	}
	d.resultCache = newLRUCache(100) // max 100 entries (~200-500KB total)

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
	word = strings.TrimSpace(word)
	if word != strings.ToLower(word) {
		word = strings.ToLower(word)
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
	startTime := time.Now()
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

	log.Printf("[LLM-DEBUG] Cache MISS for %q (took %v total)", word, time.Since(startTime))
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

// LookupWordAudio fetches the pronunciation audio URL from the Free Dictionary API.
// Returns the MP3 URL string (empty string if not found or API unavailable).
// This is designed to be called in the background after the main lookup completes,
// so it never blocks the user experience. The frontend can call this lazily and
// update the UI when the result arrives.
// The audioUrl is also merged into the cached result so subsequent lookups include it.
func (d *DictService) LookupWordAudio(word string) string {
	word = strings.TrimSpace(strings.ToLower(word))
	if word == "" {
		return ""
	}

	// Check if we already have audioUrl in the cached result
	if cached, ok := d.resultCache.get(word); ok {
		var data map[string]interface{}
		if json.Unmarshal([]byte(cached), &data) == nil {
			if audioUrl, ok := data["audioUrl"].(string); ok && audioUrl != "" {
				log.Printf("[AUDIO-DEBUG] Cache HIT for %q: %s", word, audioUrl)
				return audioUrl
			}
		}
	}

	// Query Free Dictionary API
	audioUrl := d.fetchFreeDictAudio(word)
	if audioUrl == "" {
		log.Printf("[AUDIO-DEBUG] No audio found for %q", word)
		return ""
	}

	log.Printf("[AUDIO-DEBUG] Found audio for %q: %s", word, audioUrl)

	// Merge audioUrl into the cached result (if any)
	if cached, ok := d.resultCache.get(word); ok {
		var data map[string]interface{}
		if json.Unmarshal([]byte(cached), &data) == nil {
			data["audioUrl"] = audioUrl
			if updated, err := json.Marshal(data); err == nil {
				d.resultCache.set(word, string(updated))
			}
		}
	}

	return audioUrl
}

// fetchFreeDictAudio queries the Free Dictionary API (dictionaryapi.dev) for
// pronunciation audio URLs. Prefers US accent, then UK, then any available.
// Returns empty string if no audio found or API unavailable.
func (d *DictService) fetchFreeDictAudio(word string) string {
	url := "https://api.dictionaryapi.dev/api/v2/entries/en/" + strings.ToLower(word)

	// Reuse the shared pooled client with a per-request timeout
	// (previously a brand-new client was created per call, paying a
	// full DNS+TCP+TLS handshake every time).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := d.httpClient.Do(req)
	if err != nil {
		log.Printf("[AUDIO-DEBUG] FreeDict API request failed for %q: %v", word, err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "" // 404 = not found, silently skip
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	// API returns an array of entries
	var entries []struct {
		Phonetics []struct {
			Audio string `json:"audio"`
		} `json:"phonetics"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return ""
	}

	// Collect all audio URLs, prefer US accent
	var usAudios, ukAudios, otherAudios []string
	for _, entry := range entries {
		for _, p := range entry.Phonetics {
			if p.Audio == "" {
				continue
			}
			if strings.Contains(p.Audio, "-us") {
				usAudios = append(usAudios, p.Audio)
			} else if strings.Contains(p.Audio, "-uk") {
				ukAudios = append(ukAudios, p.Audio)
			} else {
				otherAudios = append(otherAudios, p.Audio)
			}
		}
	}

	// Priority: US > UK > other
	if len(usAudios) > 0 {
		return usAudios[0]
	}
	if len(ukAudios) > 0 {
		return ukAudios[0]
	}
	if len(otherAudios) > 0 {
		return otherAudios[0]
	}

	return ""
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
	// Check LLM configuration — return a specific error so the frontend
	// can distinguish "not configured" from "call failed".
	if d.apiKey == "" || d.apiURL == "" || d.modelName == "" {
		return "", ErrLLMNotConfigured
	}
	word = strings.TrimSpace(word)
	if word != strings.ToLower(word) {
		word = strings.ToLower(word)
	}
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
					if resp.StatusCode == http.StatusForbidden {
						// 403 on Groq & similar = geographic restriction (US/UK only).
						// Common when the request goes direct instead of through the proxy.
						return "", fmt.Errorf("API错误(403): %s。提示：Groq 等服务商仅允许美国/英国地区访问，请确认 HTTP 代理已启用且节点为美/英地区", msg)
					}
					return "", fmt.Errorf("API错误: %s", msg)
				}
			}
			if msg, ok := errResp["message"].(string); ok {
				if code, ok := errResp["code"]; ok {
					return "", fmt.Errorf("API错误(code=%v): %s", code, msg)
				}
				if resp.StatusCode == http.StatusForbidden {
					return "", fmt.Errorf("API错误(403): %s。提示：Groq 等服务商仅允许美国/英国地区访问，请确认 HTTP 代理已启用且节点为美/英地区", msg)
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

// SaveConfig saves API configuration (apiKey, apiURL, modelName, shortcutKey, proxy).
// proxy: empty string means direct connection (no proxy); if invalid it's stored
// but ignored at startup (falls back to direct).
func (d *DictService) SaveConfig(apiKey, apiURL, modelName, shortcutKey, proxy string) error {
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
	d.proxy = proxy
	configPath := getConfigPath("config.json")
	data, _ := json.MarshalIndent(map[string]string{
		"apiKey":      apiKey,
		"apiURL":      d.apiURL,
		"modelName":   d.modelName,
		"shortcutKey": d.shortcutKey,
		"autoStart":   strconv.FormatBool(d.autoStart),
		"proxy":       d.proxy,
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
		"proxy":       d.proxy,
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
		"/v", "WordFlow",
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
		"/v", "WordFlow",
		"/f",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("reg delete failed: %w, output: %s", err, string(output))
	}
	log.Println("Auto-start disabled")
	return nil
}

// checkAutoStartRegistry checks if the Run key exists for WordFlow.
func checkAutoStartRegistry() bool {
	cmd := exec.Command("reg", "query",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run",
		"/v", "WordFlow",
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
// GetPendingWord returns the word captured by the last global-hotkey press
// and clears it. The frontend polls this (instead of relying only on the
// event) because WebView2 throttles the renderer while the window is hidden
// and event delivery to it can be delayed or lost.
func (d *DictService) GetPendingWord() string {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	w := d.pendingWord
	d.pendingWord = ""
	return w
}

// setPendingWord stores the word from a hotkey press for the frontend to pull.
func (d *DictService) setPendingWord(w string) {
	d.pendingMu.Lock()
	d.pendingWord = w
	d.pendingMu.Unlock()
}

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
			if ok && w.IsVisible() {
				// Window is visible — hide it (toggle off)
				log.Printf("[SHORTCUT-DEBUG] Window visible, hiding")
				w.Hide()
				return
			}
			// 1) Try UI Automation first: read the selected text of the
			//    foreground window directly (no clipboard, no Ctrl+C).
			//    Must run BEFORE showing our window, otherwise
			//    GetFocusedElement returns our own webview.
			word := getFocusedSelectionTextUIA()
			source := "uia"
			if !isEnglishText(word) {
				// 2) Fallback: existing clipboard behavior.
				text, ok := d.app.Clipboard.Text()
				log.Printf("[SHORTCUT-DEBUG] Shortcut pressed, uia=%q clipboard: ok=%v text=%q", truncate(word, 50), ok, truncate(text, 50))
				if ok && isEnglishText(text) {
					word = strings.TrimSpace(text)
					source = "clipboard"
				} else {
					word = ""
				}
			}
			// Show the window only after the UIA query, so the foreground
			// app remains the selection source while we read it.
			if ok {
				w.Show()
				w.Focus()
			}
			// Auto-lowercase: capitals from sentence-start or caps-lock are
			// not meaningful for dictionary lookup.
			word = strings.TrimSpace(word)
			if len(word) > 0 && word != strings.ToLower(word) {
				word = strings.ToLower(word)
			}
			if word != "" {
				log.Printf("[SHORTCUT-DEBUG] Emitting (source=%s): %q", source, word)
				// Store for the frontend poll AND emit the event as a fast path.
				d.setPendingWord(word)
				d.app.Event.Emit("clipboard-english-detected", word)
			} else {
				log.Printf("[SHORTCUT-DEBUG] No English selection found (uia + clipboard)")
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
	Deleted   bool   `json:"deleted"`   // Soft delete flag (synced to server)
}

// HistoryService manages the local word book using SQLite.
// It replaces the old JSON-file-based history with proper database storage,
// enabling reliable persistence and sync support.
type HistoryService struct {
	db        *sql.DB
	mu        sync.RWMutex
	once      sync.Once
	syncCb    func(entry HistoryEntry)  // callback to notify sync service (single entry)
	syncBulkCb func(entries []HistoryEntry) // callback for bulk deletions (e.g. ClearHistory)
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
			updated_at INTEGER NOT NULL DEFAULT 0,
			deleted    INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		return fmt.Errorf("创建 history 表失败: %v", err)
	}

	// Migrate: add deleted column if it doesn't exist (for existing DBs)
	var deletedCol int
	err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('history') WHERE name = 'deleted'").Scan(&deletedCol)
	if err == nil && deletedCol == 0 {
		if _, err := db.Exec("ALTER TABLE history ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0"); err != nil {
			log.Printf("Warning: add deleted column failed: %v", err)
		} else {
			log.Printf("Migration: added column history.deleted")
		}
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

	// Migrate: lowercase all capitalized words in history
	h.migrateLowercaseWords(db)

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

// migrateLowercaseWords fixes existing data where words were stored with capital letters.
// It lowercases the word column and merges duplicates that collapse to the same lowercase word.
func (h *HistoryService) migrateLowercaseWords(db *sql.DB) {
	// Use a migration flag column to ensure this only runs once
	var migrated int
	err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('history') WHERE name = 'word_lowercased'").Scan(&migrated)
	if err != nil {
		log.Printf("Warning: check word_lowercased flag failed: %v", err)
		return
	}
	if migrated > 0 {
		return // Already migrated
	}

	log.Printf("Migration: lowercasing capitalized words in local history...")

	// Find all entries where word != LOWER(word)
	rows, err := db.Query(
		"SELECT id, word, result, created_at, updated_at FROM history WHERE word != LOWER(word) AND deleted = 0",
	)
	if err != nil {
		log.Printf("Warning: query uppercase history entries failed: %v", err)
		return
	}

	type entry struct {
		id        string
		word      string
		result    string
		createdAt int64
		updatedAt int64
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.word, &e.result, &e.createdAt, &e.updatedAt); err != nil {
			log.Printf("Warning: scan history entry failed: %v", err)
			continue
		}
		entries = append(entries, e)
	}
	rows.Close()

	if len(entries) == 0 {
		log.Printf("Migration: no capitalized words found in local history")
	} else {
		log.Printf("Migration: found %d capitalized words in local history", len(entries))

		// Group by lowercase word to detect duplicates
		groups := make(map[string][]entry)
		for _, e := range entries {
			key := strings.ToLower(e.word)
			groups[key] = append(groups[key], e)
		}

		for lowerWord, group := range groups {
			if len(group) == 1 {
				// No duplicate — just update the word and result JSON
				e := group[0]
				lowerResult := lowercaseResultJSON(e.result, lowerWord)
				_, err := db.Exec(
					"UPDATE history SET word = ?, result = ? WHERE id = ?",
					lowerWord, lowerResult, e.id,
				)
				if err != nil {
					log.Printf("Warning: update word for id=%s failed: %v", e.id, err)
				}
				continue
			}

			// Multiple entries collapse to same lowercase word — keep newest, delete rest
			// Sort by updated_at descending
			for i := 1; i < len(group); i++ {
				for j := i; j > 0 && group[j].updatedAt > group[j-1].updatedAt; j-- {
					group[j], group[j-1] = group[j-1], group[j]
				}
			}

			// Keep group[0] (newest), delete the rest
			keeper := group[0]
			lowerResult := lowercaseResultJSON(keeper.result, lowerWord)
			_, err := db.Exec(
				"UPDATE history SET word = ?, result = ? WHERE id = ?",
				lowerWord, lowerResult, keeper.id,
				)
			if err != nil {
				log.Printf("Warning: update word for id=%s failed: %v", keeper.id, err)
			}

			for _, dup := range group[1:] {
				_, err := db.Exec("DELETE FROM history WHERE id = ?", dup.id)
				if err != nil {
					log.Printf("Warning: delete duplicate id=%s failed: %v", dup.id, err)
				}
				log.Printf("Migration: merged duplicate word %q → %q (kept id=%s, deleted id=%s)",
					keeper.word, lowerWord, keeper.id, dup.id)
			}
		}

		log.Printf("Migration: lowercased %d words in local history (%d duplicates merged)",
			len(entries), len(entries)-len(groups))
	}

	// Mark migration as done
	_, err = db.Exec("ALTER TABLE history ADD COLUMN word_lowercased INTEGER NOT NULL DEFAULT 1")
	if err != nil {
		log.Printf("Warning: add word_lowercased flag failed: %v", err)
	}

	log.Printf("Migration: lowercase words migration complete")
}

// lowercaseResultJSON lowercases the "word" field inside a result JSON string.
func lowercaseResultJSON(result string, lowerWord string) string {
	if result == "" {
		return result
	}
	idx := strings.Index(result, `"word":"`)
	if idx == -1 {
		return result
	}
	start := idx + len(`"word":"`)
	end := strings.Index(result[start:], `"`)
	if end == -1 {
		return result
	}
	end += start
	return result[:start] + lowerWord + result[end:]
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

	rows, err := db.Query("SELECT id, word, result, created_at, updated_at, deleted FROM history WHERE deleted = 0 ORDER BY created_at DESC")
	if err != nil {
		log.Printf("HistoryService: query error: %v", err)
		return []HistoryEntry{}
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var deleted int
		if err := rows.Scan(&e.ID, &e.Word, &e.Result, &e.CreatedAt, &e.UpdatedAt, &deleted); err != nil {
			continue
		}
		e.Deleted = deleted == 1
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

	// History words are stored lowercase (see migrateLowercaseWords).
	// Normalize here so the exact-match query below uses the index
	// instead of COLLATE NOCASE (which forces a full table scan).
	word = strings.TrimSpace(strings.ToLower(word))

	now := time.Now().Unix()

	var savedEntry HistoryEntry

	// Check if word already exists (exact match, index-friendly)
	var existingID string
	var existingDeleted int
	err := h.db.QueryRow("SELECT id, deleted FROM history WHERE word = ?", word).Scan(&existingID, &existingDeleted)

	if err == sql.ErrNoRows {
		// New entry
		id := fmt.Sprintf("%d", time.Now().UnixNano())
		_, err := h.db.Exec(
			"INSERT INTO history (id, word, result, created_at, updated_at, deleted) VALUES (?, ?, ?, ?, ?, 0)",
			id, word, result, now, now,
		)
		if err != nil {
			return err
		}
		savedEntry = HistoryEntry{ID: id, Word: word, Result: result, CreatedAt: now, UpdatedAt: now}
	} else if err == nil {
		// Update existing entry (also undelete if it was soft-deleted)
		_, err := h.db.Exec(
			"UPDATE history SET result = ?, updated_at = ?, deleted = 0 WHERE id = ?",
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

// DeleteHistory soft-deletes an entry by ID (marks as deleted for sync).
// The entry remains in the DB so the deletion can be synced to the server.
func (h *HistoryService) DeleteHistory(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.db == nil {
		return fmt.Errorf("数据库未初始化")
	}

	now := time.Now().Unix()
	_, err := h.db.Exec("UPDATE history SET deleted = 1, updated_at = ? WHERE id = ?", now, id)
	if err != nil {
		return err
	}

	// Notify sync service (non-blocking) — push the deletion to server
	if h.syncCb != nil {
		// Look up the entry to build a HistoryEntry with the deleted flag
		var e HistoryEntry
		err := h.db.QueryRow(
			"SELECT id, word, result, created_at, updated_at, deleted FROM history WHERE id = ?",
			id,
		).Scan(&e.ID, &e.Word, &e.Result, &e.CreatedAt, &e.UpdatedAt, &e.Deleted)
		if err == nil {
			go h.syncCb(e)
		}
	}

	return nil
}

// SoftDeleteFromSync marks an entry as deleted from a server pull, without
// triggering the sync callback (to avoid pushing it back to the server).
func (h *HistoryService) SoftDeleteFromSync(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.db == nil {
		return fmt.Errorf("数据库未初始化")
	}

	now := time.Now().Unix()
	_, err := h.db.Exec("UPDATE history SET deleted = 1, updated_at = ? WHERE id = ?", now, id)
	return err
}

// ClearHistory removes all entries
func (h *HistoryService) ClearHistory() error {
	h.mu.Lock()

	if h.db == nil {
		h.mu.Unlock()
		return fmt.Errorf("数据库未初始化")
	}

	now := time.Now().Unix()

	// Collect entries for sync notification before soft-deleting
	var entries []HistoryEntry
	if h.syncBulkCb != nil {
		rows, err := h.db.Query("SELECT id, word, result, created_at, updated_at FROM history WHERE deleted = 0")
		if err == nil {
			for rows.Next() {
				var e HistoryEntry
				if rows.Scan(&e.ID, &e.Word, &e.Result, &e.CreatedAt, &e.UpdatedAt) == nil {
					e.Deleted = true
					e.UpdatedAt = now
					entries = append(entries, e)
				}
			}
			rows.Close()
		}
	}

	// Soft-delete all entries (not hard DELETE) so deletions can sync
	_, err := h.db.Exec("UPDATE history SET deleted = 1, updated_at = ? WHERE deleted = 0", now)
	h.mu.Unlock()

	if err != nil {
		return err
	}

	// Notify sync service with bulk callback (single batch push)
	if h.syncBulkCb != nil && len(entries) > 0 {
		h.syncBulkCb(entries)
	}

	return nil
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
	var deleted int
	err := db.QueryRow(
		"SELECT id, word, result, created_at, updated_at, deleted FROM history WHERE id = ?",
		id,
	).Scan(&e.ID, &e.Word, &e.Result, &e.CreatedAt, &e.UpdatedAt, &deleted)
	e.Deleted = deleted == 1

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

	// History words are stored lowercase; normalize input so the
	// exact-match query uses the idx_history_word index.
	word = strings.TrimSpace(strings.ToLower(word))

	var e HistoryEntry
	var deleted int
	err := db.QueryRow(
		"SELECT id, word, result, created_at, updated_at, deleted FROM history WHERE word = ? AND deleted = 0",
		word,
	).Scan(&e.ID, &e.Word, &e.Result, &e.CreatedAt, &e.UpdatedAt, &deleted)
	e.Deleted = deleted == 1

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

	rows, err := db.Query("SELECT id, word, result, created_at, updated_at, deleted FROM history ORDER BY updated_at ASC")
	if err != nil {
		return []HistoryEntry{}
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var deleted int
		if err := rows.Scan(&e.ID, &e.Word, &e.Result, &e.CreatedAt, &e.UpdatedAt, &deleted); err != nil {
			continue
		}
		e.Deleted = deleted == 1
		entries = append(entries, e)
	}
	return entries
}

// ============================================================
// SyncService - PC sync client (connects to remote sync server)
// Supports both legacy token auth and WeChat QR code login
// ============================================================

type SyncService struct {
	history      *HistoryService
	syncAddr     string // Remote sync server address, e.g. https://word-flow.duckdns.org:31588/
	syncToken    string // User Token (assigned by server via QR code login, email login, or legacy create)
	syncQrCode   string // Cached QR code image (base64 data URL) from last successful login
	syncEmail    string // Email address used for login (if email auth was used)
	autoSync     bool   // Whether auto-sync is enabled
	lastSyncTime int64  // Unix timestamp of last successful sync (for incremental pull)

	syncClient *http.Client // HTTP/3-capable client for sync server communication
}

func (s *SyncService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.syncClient = syncserver.NewHTTP3Client(30 * time.Second)
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
		SyncQrCode   string `json:"syncQrCode"`
		SyncEmail    string `json:"syncEmail"`
		AutoSync     bool   `json:"autoSync"`
		LastSyncTime int64  `json:"lastSyncTime"`
	}
	if json.Unmarshal(data, &cfg) == nil {
		s.syncAddr = cfg.SyncAddr
		s.syncToken = cfg.SyncToken
		s.syncQrCode = cfg.SyncQrCode
		s.syncEmail = cfg.SyncEmail
		s.autoSync = cfg.AutoSync
		s.lastSyncTime = cfg.LastSyncTime
	}
}

func (s *SyncService) saveConfig() error {
	configPath := getConfigPath("sync_config.json")
	data, _ := json.MarshalIndent(map[string]interface{}{
		"syncAddr":     s.syncAddr,
		"syncToken":    s.syncToken,
		"syncQrCode":   s.syncQrCode,
		"syncEmail":    s.syncEmail,
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
		"syncQrCode": s.syncQrCode,
		"syncEmail": s.syncEmail,
		"autoSync":  autoSyncStr,
	}
}

// SaveSyncConfig saves sync configuration.
// If syncToken is empty and a token is already stored, the existing token is preserved.
// Pass a non-empty syncToken to update it, or call UnlinkSync to clear it.
func (s *SyncService) SaveSyncConfig(syncAddr, syncToken string, autoSync bool) error {
	s.syncAddr = syncAddr
	if syncToken != "" {
		s.syncToken = syncToken
	} else if s.syncToken != "" {
		// Preserve existing token when frontend passes empty (linked state)
	}
	s.autoSync = autoSync
	return s.saveConfig()
}

// UnlinkSync clears the sync token and QR code, allowing re-linking to a new account.
func (s *SyncService) UnlinkSync() error {
	s.syncToken = ""
	s.syncQrCode = ""
	s.syncEmail = ""
	return s.saveConfig()
}

// TestConnection tests the connection to the sync server.
// Returns a human-readable status message.
func (s *SyncService) TestConnection() (string, error) {
	if s.syncAddr == "" {
		return "", fmt.Errorf("please set sync server address first")
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/health"
	client := s.syncClient
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("cannot connect to server: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("server returned abnormal status: HTTP %d", resp.StatusCode)
	}

	var healthResult struct {
		Status  string `json:"status"`
		Service string `json:"service"`
		Version string `json:"version"`
		Wechat  bool   `json:"wechat"`
	}
	json.NewDecoder(resp.Body).Decode(&healthResult)

	status := fmt.Sprintf("Connected: %s v%s", healthResult.Service, healthResult.Version)
	if healthResult.Wechat {
		status += " (WeChat auth enabled)"
	}

	// If token is set, also check user status
	if s.syncToken != "" {
		statusURL := strings.TrimRight(s.syncAddr, "/") + "/api/v1/user/status"
		req, _ := http.NewRequest("GET", statusURL, nil)
		req.Header.Set("Authorization", "Bearer "+s.syncToken)
		resp2, err := client.Do(req)
		if err != nil {
			return status + " | Token validation failed", nil
		}
		defer resp2.Body.Close()

		if resp2.StatusCode == 200 {
			var syncStatus syncserver.SyncStatusResponse
			if json.NewDecoder(resp2.Body).Decode(&syncStatus) == nil {
				return fmt.Sprintf("%s | Synced %d words", status, syncStatus.WordCount), nil
			}
		}
		return status + " | Token invalid (re-login needed)", nil
	}

	return status + " | Not logged in yet", nil
}

// RequestQrCode requests a QR code login session from the sync server.
// Returns a map with "scene", "expiresIn", and optionally "qrcode" (base64 PNG data URL).
// The desktop app should display the QR code image (or the scene string as fallback)
// and then poll with PollQrCodeStatus until the user scans it.
func (s *SyncService) RequestQrCode() (map[string]interface{}, error) {
	if s.syncAddr == "" {
		return nil, fmt.Errorf("please set sync server address first")
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/auth/qrcode/request"
	client := s.syncClient
	resp, err := client.Post(url, "application/json", strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusServiceUnavailable {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil {
			return nil, fmt.Errorf("WeChat auth not available: %s", errResp.Error)
		}
		return nil, fmt.Errorf("WeChat auth not configured on server")
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("server error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response failed: %v", err)
	}

	log.Printf("SyncService: QR code requested, scene=%v, expiresIn=%v",
		result["scene"], result["expiresIn"])

	// Cache the QR code image in memory so we can persist it on successful login
	if qrcode, ok := result["qrcode"].(string); ok && qrcode != "" {
		s.syncQrCode = qrcode
	}

	return result, nil
}

// PollQrCodeStatus polls the sync server for QR code login status.
// Returns a map with "status" ("pending" | "scanned" | "expired") and
// optionally "token" (when status is "scanned").
// If the user has scanned the QR code, this method auto-saves the token.
func (s *SyncService) PollQrCodeStatus(scene string) (map[string]interface{}, error) {
	if s.syncAddr == "" {
		return nil, fmt.Errorf("please set sync server address first")
	}
	if scene == "" {
		return nil, fmt.Errorf("scene parameter is required")
	}

	url := fmt.Sprintf("%s/api/v1/auth/qrcode/status?scene=%s",
		strings.TrimRight(s.syncAddr, "/"), scene)

	client := s.syncClient
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("poll failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return map[string]interface{}{"status": "expired"}, nil
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("server error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response failed: %v", err)
	}

	// Auto-save token and QR code if login is complete
	if status, ok := result["status"].(string); ok && status == "scanned" {
		if token, ok := result["token"].(string); ok && token != "" {
			s.syncToken = token
			s.saveConfig()
			log.Printf("SyncService: QR code login complete, token and QR code saved")
		}
	}

	return result, nil
}

// RequestEmailCode requests a verification code be sent to the given email.
// Returns a human-readable message (e.g. "Verification code sent to your email").
func (s *SyncService) RequestEmailCode(email string) (string, error) {
	if s.syncAddr == "" {
		return "", fmt.Errorf("please set sync server address first")
	}
	if email == "" {
		return "", fmt.Errorf("email is required")
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/auth/email/request"
	body, _ := json.Marshal(map[string]string{"email": email})

	client := s.syncClient
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		var errResp map[string]interface{}
		if json.Unmarshal(respBody, &errResp) == nil {
			if msg, ok := errResp["error"].(string); ok {
				return "", fmt.Errorf(msg)
			}
		}
		return "", fmt.Errorf("server error (HTTP %d)", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response failed: %v", err)
	}

	msg, _ := result["message"].(string)
	if msg == "" {
		msg = "Verification code sent"
	}
	return msg, nil
}

// VerifyEmailCode verifies the email code and saves the token on success.
// Returns a human-readable message.
func (s *SyncService) VerifyEmailCode(email, code string) (string, error) {
	if s.syncAddr == "" {
		return "", fmt.Errorf("please set sync server address first")
	}
	if email == "" || code == "" {
		return "", fmt.Errorf("email and code are required")
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/auth/email/verify"
	body, _ := json.Marshal(map[string]string{"email": email, "code": code})

	client := s.syncClient
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("verify failed: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		var errResp map[string]interface{}
		if json.Unmarshal(respBody, &errResp) == nil {
			if msg, ok := errResp["error"].(string); ok {
				return "", fmt.Errorf(msg)
			}
		}
		return "", fmt.Errorf("server error (HTTP %d)", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response failed: %v", err)
	}

	token, _ := result["token"].(string)
	if token == "" {
		msg, _ := result["message"].(string)
		if msg == "" {
			msg = "Verification failed"
		}
		return msg, fmt.Errorf("verification failed")
	}

	// Save token and email
	s.syncToken = token
	s.syncEmail = email
	s.syncQrCode = "" // Clear QR code since we used email auth
	s.saveConfig()
	log.Printf("SyncService: email login complete, token saved for %s", email)

	return "Login successful! ✅", nil
}

// RequestPairCode generates a pairing code on the server for this user.
// Returns the 6-digit code string.
func (s *SyncService) RequestPairCode() (string, error) {
	if s.syncAddr == "" {
		return "", fmt.Errorf("please set sync server address first")
	}
	if s.syncToken == "" {
		return "", fmt.Errorf("please log in first")
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/auth/pair/request"

	client := s.syncClient
	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("Authorization", "Bearer "+s.syncToken)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		var errResp map[string]interface{}
		if json.Unmarshal(respBody, &errResp) == nil {
			if msg, ok := errResp["error"].(string); ok {
				return "", fmt.Errorf(msg)
			}
		}
		return "", fmt.Errorf("server error (HTTP %d)", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response failed: %v", err)
	}

	code, _ := result["code"].(string)
	if code == "" {
		return "", fmt.Errorf("server did not return a pairing code")
	}
	return code, nil
}
func (s *SyncService) CreateUser() (string, error) {
	if s.syncAddr == "" {
		return "", fmt.Errorf("please set sync server address first")
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/user/create"
	client := s.syncClient
	resp, err := client.Post(url, "application/json", strings.NewReader("{}"))
	if err != nil {
		return "", fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create user failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Token   string `json:"token"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse response failed: %v", err)
	}

	// Auto-save the token
	s.syncToken = result.Token
	s.saveConfig()

	return result.Token, nil
}

// PushToServer pushes all local history entries to the remote sync server.
func (s *SyncService) PushToServer() (string, error) {
	if s.syncAddr == "" || s.syncToken == "" {
		return "", fmt.Errorf("please configure sync server address and token first")
	}
	if s.history == nil {
		return "", fmt.Errorf("history service not initialized")
	}

	entries := s.history.GetAllEntriesForSync()
	if len(entries) == 0 {
		return "No data to sync", nil
	}

	// Convert to sync entries (include deleted flag for proper sync)
	syncEntries := make([]syncserver.SyncEntry, len(entries))
	for i, e := range entries {
		syncEntries[i] = syncserver.SyncEntry{
			ID:        e.ID,
			Word:      e.Word,
			Result:    e.Result,
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.UpdatedAt,
			Deleted:   e.Deleted,
		}
	}

	reqBody := syncserver.SyncPushRequest{Entries: syncEntries}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("build request failed: %v", err)
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/sync/push"
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("create request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.syncToken)

	client := s.syncClient
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("token invalid, please re-login")
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("server error: HTTP %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if json.NewDecoder(resp.Body).Decode(&result) == nil {
		if upserted, ok := result["upserted"]; ok {
			return fmt.Sprintf("Pushed %v entries", upserted), nil
		}
	}

	return "Push complete", nil
}

// PullFromServer pulls entries from the remote sync server and merges into local history.
// Uses incremental sync: only pulls entries updated since the last successful sync.
// Falls back to full pull if never synced before.
func (s *SyncService) PullFromServer() (string, error) {
	return s.pullFromServerInternal(false)
}

func (s *SyncService) pullFromServerInternal(silent bool) (string, error) {
	if s.syncAddr == "" || s.syncToken == "" {
		return "", fmt.Errorf("please configure sync server address and token first")
	}
	if s.history == nil {
		return "", fmt.Errorf("history service not initialized")
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
		return "", fmt.Errorf("create request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.syncToken)

	client := s.syncClient
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("token invalid, please re-login")
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("server error: HTTP %d", resp.StatusCode)
	}

	var result syncserver.SyncPullResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse response failed: %v", err)
	}

	if len(result.Entries) == 0 {
		// No new data, but still update lastSyncTime to avoid re-fetching
		if result.ServerNow > 0 {
			s.lastSyncTime = result.ServerNow
			s.saveConfig()
		}
		return "No new data on server", nil
	}

	// Merge into local history (using AddHistoryFromSync to avoid push-back loop)
	merged := 0
	isFullSync := since == 0

	// For full sync, build a set of server entry IDs for reconciliation
	serverIDs := make(map[string]bool)
	if isFullSync {
		for _, e := range result.Entries {
			serverIDs[e.ID] = true
		}
	}

	for _, e := range result.Entries {
		if e.Deleted {
			s.history.SoftDeleteFromSync(e.ID)
			continue
		}
		s.history.AddHistoryFromSync(e.Word, e.Result)
		merged++
	}

	// Full sync: soft-delete local entries not present on server (orphans)
	if isFullSync {
		localEntries := s.history.GetAllEntriesForSync()
		for _, e := range localEntries {
			if !e.Deleted && !serverIDs[e.ID] {
				s.history.SoftDeleteFromSync(e.ID)
				merged++
			}
		}
	}

	// Save lastSyncTime from server response for incremental sync
	if result.ServerNow > 0 {
		s.lastSyncTime = result.ServerNow
		s.saveConfig()
	}

	log.Printf("SyncService: pulled %d entries (since=%d, serverNow=%d)", merged, since, result.ServerNow)

	return fmt.Sprintf("Pulled and merged %d entries", merged), nil
}

// OnEntryAdded is called by HistoryService when a new entry is saved or deleted.
// If auto-sync is enabled, it pushes the entry to the server in the background.
func (s *SyncService) OnEntryAdded(entry HistoryEntry) {
	if !s.autoSync || s.syncAddr == "" || s.syncToken == "" {
		return
	}
	go s.pushEntryAsync(entry)
}

// OnEntriesDeleted is called by HistoryService when multiple entries are bulk-deleted
// (e.g. ClearHistory). Pushes all deletions in a single batch request.
func (s *SyncService) OnEntriesDeleted(entries []HistoryEntry) {
	if !s.autoSync || s.syncAddr == "" || s.syncToken == "" || len(entries) == 0 {
		return
	}
	go s.pushEntriesAsync(entries)
}

// pushEntryAsync pushes a single entry to the server in the background.
// Handles both new/updated entries and deletions (entry.Deleted = true).
func (s *SyncService) pushEntryAsync(entry HistoryEntry) {
	syncEntry := syncserver.SyncEntry{
		ID:        entry.ID,
		Word:      entry.Word,
		Result:    entry.Result,
		CreatedAt: entry.CreatedAt,
		UpdatedAt: entry.UpdatedAt,
		Deleted:   entry.Deleted,
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

	client := s.syncClient
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

	if entry.Deleted {
		log.Printf("SyncService: auto-pushed deletion of '%s' (id=%s) to server", entry.Word, entry.ID)
	} else {
		log.Printf("SyncService: auto-pushed '%s' to server", entry.Word)
	}
}

// pushEntriesAsync pushes multiple entries to the server in a single batch request.
// Used for bulk operations like ClearHistory.
func (s *SyncService) pushEntriesAsync(entries []HistoryEntry) {
	syncEntries := make([]syncserver.SyncEntry, len(entries))
	for i, e := range entries {
		syncEntries[i] = syncserver.SyncEntry{
			ID:        e.ID,
			Word:      e.Word,
			Result:    e.Result,
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.UpdatedAt,
			Deleted:   e.Deleted,
		}
	}

	reqBody := syncserver.SyncPushRequest{Entries: syncEntries}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("SyncService: batch-push marshal error: %v", err)
		return
	}

	url := strings.TrimRight(s.syncAddr, "/") + "/api/v1/sync/push"
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		log.Printf("SyncService: batch-push request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.syncToken)

	client := s.syncClient
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("SyncService: batch-push failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		log.Printf("SyncService: batch-push token invalid")
		return
	}
	if resp.StatusCode != 200 {
		log.Printf("SyncService: batch-push server error: HTTP %d", resp.StatusCode)
		return
	}

	log.Printf("SyncService: batch-pushed %d deletions to server", len(entries))
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
