package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

func main() {
	app := application.New(application.Options{
		Name:        "WordWise",
		Description: "英语词典助手 - 系统托盘 + 全局快捷键 + LLM智能查词",
		Services: []application.Service{
			application.NewService(&DictService{}),
			application.NewService(&HistoryService{}),
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
// DictService - LLM dictionary lookup
// ============================================================

type DictService struct {
	app         *application.App
	apiKey      string
	apiURL      string
	modelName   string
	shortcutKey string
	ready       bool
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

// LookupWord calls LLM to get word definition
func (d *DictService) LookupWord(word string) (string, error) {
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

请确保返回纯JSON，不要有其他内容。如果有多个词性，请在definitions中分别列出。`, word, word)

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
	// If the URL doesn't end with /chat/completions, append it
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
			// Try xfyun format: {"code":xxx, "message":"..."}
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
	// Clean up common LLM formatting issues
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	// Remove any leading/trailing text outside the JSON object
	if idx := strings.Index(content, "{"); idx >= 0 {
		if idx > 0 {
			content = content[idx:]
		}
	}
	if idx := strings.LastIndex(content, "}"); idx >= 0 {
		content = content[:idx+1]
	}
	// Remove control characters except newline/tab
	var b strings.Builder
	for _, ch := range content {
		if ch >= 0x20 || ch == '\n' || ch == '\r' || ch == '\t' {
			b.WriteRune(ch)
		}
	}
	content = strings.TrimSpace(b.String())
	return content, nil
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
	// If shortcut changed, re-register
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
// HistoryService - manages lookup history
// ============================================================

type HistoryEntry struct {
	ID        string `json:"id"`
	Word      string `json:"word"`
	Result    string `json:"result"`
	CreatedAt string `json:"createdAt"`
}

type HistoryService struct {
	entries []HistoryEntry
}

func (h *HistoryService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	return h.load()
}

func (h *HistoryService) load() error {
	p := getConfigPath("history.json")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			h.entries = []HistoryEntry{}
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &h.entries)
}

func (h *HistoryService) save() error {
	p := getConfigPath("history.json")
	data, _ := json.MarshalIndent(h.entries, "", "  ")
	return os.WriteFile(p, data, 0644)
}

// GetHistory returns all entries (newest first)
func (h *HistoryService) GetHistory() []HistoryEntry {
	result := make([]HistoryEntry, len(h.entries))
	copy(result, h.entries)
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// AddHistory adds a new entry
func (h *HistoryService) AddHistory(word, result string) error {
	h.entries = append(h.entries, HistoryEntry{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Word:      word,
		Result:    result,
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	})
	if len(h.entries) > 500 {
		h.entries = h.entries[len(h.entries)-500:]
	}
	return h.save()
}

// DeleteHistory removes an entry by ID
func (h *HistoryService) DeleteHistory(id string) error {
	for i, e := range h.entries {
		if e.ID == id {
			h.entries = append(h.entries[:i], h.entries[i+1:]...)
			break
		}
	}
	return h.save()
}

// ClearHistory removes all entries
func (h *HistoryService) ClearHistory() error {
	h.entries = []HistoryEntry{}
	return h.save()
}

// GetHistoryEntry returns a single entry by ID
func (h *HistoryService) GetHistoryEntry(id string) *HistoryEntry {
	for _, e := range h.entries {
		if e.ID == id {
			return &e
		}
	}
	return nil
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
