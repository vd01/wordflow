package syncserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Server is the HTTP sync server
type Server struct {
	store    *Store
	addr     string
	server   *http.Server
	tokenTTL time.Duration // Token display TTL (for info only, tokens don't expire)
}

// NewServer creates a new sync server
func NewServer(store *Store, addr string) *Server {
	if addr == "" {
		addr = ":9274" // Default port: "W"=9, "S"=7, "4"=4 → WordWise Sync
	}
	return &Server{
		store:    store,
		addr:     addr,
		tokenTTL: 0, // Tokens don't expire
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/v1/user/create", s.handleCreateUser)
	mux.HandleFunc("/api/v1/user/status", s.handleGetStatus)
	mux.HandleFunc("/api/v1/sync/push", s.handlePush)
	mux.HandleFunc("/api/v1/sync/pull", s.handlePull)
	mux.HandleFunc("/api/v1/sync/delete", s.handleDelete)

	// Health check
	mux.HandleFunc("/api/v1/health", s.handleHealth)

	// CORS & auth middleware
	handler := s.corsMiddleware(s.authMiddleware(mux))

	s.server = &http.Server{
		Addr:         s.addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("WordWise 同步服务器启动于 %s", s.addr)
	log.Printf("API 端点:")
	log.Printf("  POST /api/v1/user/create  - 创建新用户(获取token)")
	log.Printf("  GET  /api/v1/user/status   - 查看用户同步状态")
	log.Printf("  POST /api/v1/sync/push     - 推送单词数据到服务器")
	log.Printf("  GET  /api/v1/sync/pull      - 拉取单词数据(支持增量同步)")
	log.Printf("  POST /api/v1/sync/delete    - 删除指定条目")
	log.Printf("  GET  /api/v1/health         - 健康检查")

	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() {
	if s.server != nil {
		s.server.Close()
	}
}

// Addr returns the server address
func (s *Server) Addr() string {
	return s.addr
}

// ============================================================
// Middleware
// ============================================================

// corsMiddleware adds CORS headers for mobile app access
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Sync-Token")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// authMiddleware validates the token for protected routes
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Public routes (no auth needed)
		publicRoutes := map[string]bool{
			"/api/v1/user/create": true,
			"/api/v1/health":      true,
		}

		if publicRoutes[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		// Extract token from header
		token := extractToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "缺少认证token，请在 Authorization 或 X-Sync-Token 头中提供")
			return
		}

		if !s.store.ValidateToken(token) {
			writeError(w, http.StatusUnauthorized, "无效的token，请重新获取")
			return
		}

		// Store token in context for handlers to use
		ctx := r.Context()
		ctx = contextWithToken(ctx, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractToken gets the token from Authorization header or X-Sync-Token header
// Supports: "Bearer <token>", "Token <token>", or raw token in X-Sync-Token
func extractToken(r *http.Request) string {
	// Try X-Sync-Token header first
	if token := r.Header.Get("X-Sync-Token"); token != "" {
		return strings.TrimSpace(token)
	}

	// Try Authorization header
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}

	auth = strings.TrimSpace(auth)

	// Bearer token
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}

	// Token prefix
	if strings.HasPrefix(auth, "Token ") {
		return strings.TrimSpace(auth[6:])
	}

	// Raw token
	return auth
}

// ============================================================
// Handlers
// ============================================================

// handleCreateUser creates a new user and returns a token
// POST /api/v1/user/create
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 POST 方法")
		return
	}

	token, err := s.store.CreateUser()
	if err != nil {
		log.Printf("创建用户失败: %v", err)
		writeError(w, http.StatusInternalServerError, "创建用户失败: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"token":     token,
		"message":   "用户创建成功，请妥善保管token，它相当于你的账号密码",
		"createdAt": time.Now().Unix(),
	})
}

// handleGetStatus returns the sync status for the authenticated user
// GET /api/v1/user/status
func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 GET 方法")
		return
	}

	token := tokenFromContext(r.Context())
	status, err := s.store.GetUserStatus(token)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, status)
}

// handlePush pushes entries from client to server
// POST /api/v1/sync/push
// Body: { "entries": [...] }
func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 POST 方法")
		return
	}

	token := tokenFromContext(r.Context())

	// Limit request body size (10MB)
	r.Body = http.MaxBytesReader(w, r.Body, 10*1024*1024)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取请求体失败")
		return
	}
	defer r.Body.Close()

	var req SyncPushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON解析失败: "+err.Error())
		return
	}

	if len(req.Entries) == 0 {
		writeError(w, http.StatusBadRequest, "entries 不能为空")
		return
	}

	if len(req.Entries) > 1000 {
		writeError(w, http.StatusBadRequest, "单次推送最多1000条记录")
		return
	}

	upserted, err := s.store.PushEntries(token, req.Entries)
	if err != nil {
		log.Printf("推送失败: %v", err)
		writeError(w, http.StatusInternalServerError, "推送失败: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"upserted": upserted,
		"total":    len(req.Entries),
		"message":  fmt.Sprintf("成功同步 %d 条记录", upserted),
	})
}

// handlePull pulls entries from server to client
// GET /api/v1/sync/pull?since=<timestamp>
// If since is 0 or omitted, returns all non-deleted entries (initial sync)
func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 GET 方法")
		return
	}

	token := tokenFromContext(r.Context())

	var since int64 = 0
	if s := r.URL.Query().Get("since"); s != "" {
		if _, err := fmt.Sscanf(s, "%d", &since); err != nil {
			writeError(w, http.StatusBadRequest, "since 参数格式错误，应为Unix时间戳")
			return
		}
	}

	entries, err := s.store.PullEntries(token, since)
	if err != nil {
		log.Printf("拉取失败: %v", err)
		writeError(w, http.StatusInternalServerError, "拉取失败: "+err.Error())
		return
	}

	if entries == nil {
		entries = []SyncEntry{}
	}

	writeJSON(w, http.StatusOK, SyncPullResponse{
		Entries:   entries,
		ServerNow: time.Now().Unix(),
	})
}

// handleDelete soft-deletes an entry
// POST /api/v1/sync/delete
// Body: { "id": "entry-id" }
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 POST 方法")
		return
	}

	token := tokenFromContext(r.Context())

	var req struct {
		ID string `json:"id"`
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取请求体失败")
		return
	}
	defer r.Body.Close()

	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON解析失败: "+err.Error())
		return
	}

	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id 不能为空")
		return
	}

	if err := s.store.DeleteEntry(token, req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "删除成功",
	})
}

// handleHealth returns server health status
// GET /api/v1/health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"service": "wordwise-sync",
		"version": "1.0.0",
		"time":    time.Now().Unix(),
	})
}

// ============================================================
// Helpers
// ============================================================

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Warning: writeJSON failed: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]interface{}{
		"error":  message,
		"status": status,
	})
}
