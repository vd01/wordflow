package syncserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Server is the HTTP sync server
type Server struct {
	store     *Store
	addr      string
	server    *http.Server
	tokenTTL  time.Duration // Token display TTL (for info only, tokens don't expire)
	appID     string        // WeChat Mini Program AppID
	appSecret string        // WeChat Mini Program AppSecret

	// WeChat access_token cache
	accessTokenMu    sync.RWMutex
	accessToken      string
	accessTokenExpire time.Time
}

// NewServer creates a new sync server
func NewServer(store *Store, addr string, appID, appSecret string) *Server {
	if addr == "" {
		addr = ":9274" // Default port: "W"=9, "S"=7, "4"=4 → WordWise Sync
	}
	return &Server{
		store:     store,
		addr:      addr,
		tokenTTL:  0, // Tokens don't expire
		appID:     appID,
		appSecret: appSecret,
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

	// WeChat QR code auth routes
	mux.HandleFunc("/api/v1/auth/qrcode/request", s.handleQrCodeRequest)
	mux.HandleFunc("/api/v1/auth/qrcode/status", s.handleQrCodeStatus)
	mux.HandleFunc("/api/v1/auth/wechat/login", s.handleWeChatLogin)

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

	log.Printf("WordWise sync server started on %s", s.addr)
	log.Printf("API endpoints:")
	log.Printf("  POST /api/v1/user/create         - Create new user (legacy, get token)")
	log.Printf("  POST /api/v1/auth/qrcode/request  - Request QR code login (get scene)")
	log.Printf("  GET  /api/v1/auth/qrcode/status   - Poll QR code login status")
	log.Printf("  POST /api/v1/auth/wechat/login    - WeChat mini program login")
	log.Printf("  GET  /api/v1/user/status          - View user sync status")
	log.Printf("  POST /api/v1/sync/push            - Push word data to server")
	log.Printf("  GET  /api/v1/sync/pull            - Pull word data (incremental sync)")
	log.Printf("  POST /api/v1/sync/delete          - Delete entry")
	log.Printf("  GET  /api/v1/health               - Health check")
	if s.appID != "" {
		log.Printf("  WeChat AppID: %s", s.appID)
	} else {
		log.Printf("  WeChat AppID: (not configured, QR code auth disabled)")
	}

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
			"/api/v1/user/create":         true,
			"/api/v1/auth/qrcode/request": true,
			"/api/v1/auth/qrcode/status":  true,
			"/api/v1/auth/wechat/login":   true,
			"/api/v1/health":              true,
		}

		if publicRoutes[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		// Extract token from header
		token := extractToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "Missing auth token, provide in Authorization or X-Sync-Token header")
			return
		}

		if !s.store.ValidateToken(token) {
			writeError(w, http.StatusUnauthorized, "Invalid token, please re-authenticate")
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
// WeChat API helpers
// ============================================================

// wechatAccessTokenResponse is the response from WeChat's access_token API
type wechatAccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

// wechatCode2SessionResponse is the response from WeChat's code2Session API
type wechatCode2SessionResponse struct {
	OpenID     string `json:"openid"`
	SessionKey string `json:"session_key"`
	UnionID    string `json:"unionid"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

// getWeChatAccessToken fetches a valid access_token, using cache when possible.
func (s *Server) getWeChatAccessToken() (string, error) {
	if s.appID == "" || s.appSecret == "" {
		return "", fmt.Errorf("WeChat AppID/AppSecret not configured")
	}

	// Check cache
	s.accessTokenMu.RLock()
	if s.accessToken != "" && time.Now().Before(s.accessTokenExpire) {
		token := s.accessToken
		s.accessTokenMu.RUnlock()
		return token, nil
	}
	s.accessTokenMu.RUnlock()

	// Fetch new access_token
	url := fmt.Sprintf(
		"https://api.weixin.qq.com/cgi-bin/token?grant_type=client_credential&appid=%s&secret=%s",
		s.appID, s.appSecret,
	)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("request WeChat access_token failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read WeChat access_token response failed: %v", err)
	}

	var result wechatAccessTokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse WeChat access_token response failed: %v", err)
	}

	if result.ErrCode != 0 {
		return "", fmt.Errorf("WeChat access_token error: errcode=%d, errmsg=%s", result.ErrCode, result.ErrMsg)
	}

	// Cache with 5-minute safety margin
	s.accessTokenMu.Lock()
	s.accessToken = result.AccessToken
	s.accessTokenExpire = time.Now().Add(time.Duration(result.ExpiresIn-300) * time.Second)
	s.accessTokenMu.Unlock()

	log.Printf("WeChat access_token refreshed, expires_in=%ds", result.ExpiresIn)
	return result.AccessToken, nil
}

// code2Session calls WeChat's auth.code2Session API to exchange a wx.login code for openid.
func (s *Server) code2Session(code string) (*wechatCode2SessionResponse, error) {
	if s.appID == "" || s.appSecret == "" {
		return nil, fmt.Errorf("WeChat AppID/AppSecret not configured")
	}

	url := fmt.Sprintf(
		"https://api.weixin.qq.com/sns/jscode2session?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code",
		s.appID, s.appSecret, code,
	)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("request WeChat code2Session failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read WeChat code2Session response failed: %v", err)
	}

	var result wechatCode2SessionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse WeChat code2Session response failed: %v", err)
	}

	if result.ErrCode != 0 {
		return nil, fmt.Errorf("WeChat code2Session error: errcode=%d, errmsg=%s", result.ErrCode, result.ErrMsg)
	}

	return &result, nil
}

// generateWeChatQrCode calls wxacode.getUnlimited to generate a mini program QR code image.
// Returns the image bytes (PNG) or an error.
func (s *Server) generateWeChatQrCode(scene string) ([]byte, error) {
	accessToken, err := s.getWeChatAccessToken()
	if err != nil {
		return nil, fmt.Errorf("get access_token failed: %v", err)
	}

	url := fmt.Sprintf(
		"https://api.weixin.qq.com/wxa/getwxacodeunlimit?access_token=%s",
		accessToken,
	)

	// Request body for wxacode.getUnlimited
	reqBody := map[string]interface{}{
		"scene":       scene,
		"page":        "pages/index/index",
		"width":       280,
		"env_version": "trial",   // Open experience/trial version for testers
		"check_path":  false,     // Don't verify page exists in published version
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal QR code request failed: %v", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("request WeChat QR code failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read WeChat QR code response failed: %v", err)
	}

	// Check if response is an error (JSON) or an image (PNG)
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") || (len(body) > 0 && body[0] == '{') {
		// Error response
		var errResp struct {
			ErrCode int    `json:"errcode"`
			ErrMsg  string `json:"errmsg"`
		}
		if jsonErr := json.Unmarshal(body, &errResp); jsonErr == nil && errResp.ErrCode != 0 {
			return nil, fmt.Errorf("WeChat QR code error: errcode=%d, errmsg=%s", errResp.ErrCode, errResp.ErrMsg)
		}
		return nil, fmt.Errorf("WeChat QR code returned unexpected JSON: %s", string(body))
	}

	log.Printf("WeChat QR code generated for scene=%s, size=%d bytes", scene, len(body))
	return body, nil
}

// ============================================================
// Handlers
// ============================================================

// handleCreateUser creates a new user and returns a token (legacy endpoint, kept for backward compat)
// POST /api/v1/user/create
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, http.StatusMethodNotAllowed, "Only POST method is supported")
		return
	}

	token, err := s.store.CreateUser()
	if err != nil {
		log.Printf("Create user failed: %v", err)
		writeError(w, http.StatusInternalServerError, "Create user failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"token":     token,
		"message":   "User created. Keep your token safe — it acts as your account password",
		"createdAt": time.Now().Unix(),
	})
}

// handleQrCodeRequest initiates a QR code login session.
// POST /api/v1/auth/qrcode/request
// Response: { "scene": "A3F8K2M1", "expiresIn": 300, "qrcode": "<base64 PNG>" }
// If WeChat QR code generation fails, qrcode will be empty and the desktop app
// can display the scene string as a fallback pairing code.
func (s *Server) handleQrCodeRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, http.StatusMethodNotAllowed, "Only POST method is supported")
		return
	}

	if s.appID == "" || s.appSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "WeChat auth not configured on server (missing WECHAT_APP_ID / WECHAT_APP_SECRET)")
		return
	}

	const sessionTTL = 5 * time.Minute

	// Create auth session in DB
	scene, err := s.store.CreateAuthSession(sessionTTL)
	if err != nil {
		log.Printf("Create auth session failed: %v", err)
		writeError(w, http.StatusInternalServerError, "Create auth session failed: "+err.Error())
		return
	}

	// Try to generate WeChat QR code image
	var qrcodeBase64 string
	qrBytes, err := s.generateWeChatQrCode(scene)
	if err != nil {
		log.Printf("WeChat QR code generation failed (scene=%s): %v", scene, err)
		log.Printf("Falling back to pairing code mode — desktop app should display the scene string")
		// qrcodeBase64 stays empty — desktop app will use scene as pairing code
	} else {
		qrcodeBase64 = "data:image/png;base64," + encodeBase64(qrBytes)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"scene":     scene,
		"expiresIn": int(sessionTTL.Seconds()),
		"qrcode":    qrcodeBase64, // empty string if generation failed
	})
}

// handleQrCodeStatus polls the status of a QR code login session.
// GET /api/v1/auth/qrcode/status?scene=A3F8K2M1
// Response: { "status": "pending" | "scanned" | "expired", "token": "..." (only when scanned) }
func (s *Server) handleQrCodeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, http.StatusMethodNotAllowed, "Only GET method is supported")
		return
	}

	scene := r.URL.Query().Get("scene")
	if scene == "" {
		writeError(w, http.StatusBadRequest, "Missing scene parameter")
		return
	}

	sess, err := s.store.GetAuthSession(scene)
	if err != nil {
		writeError(w, http.StatusNotFound, "Session not found or expired")
		return
	}

	response := map[string]interface{}{
		"status": sess.Status,
	}

	// Only include token when the session has been scanned (user logged in)
	if sess.Status == "scanned" && sess.Token != "" {
		response["token"] = sess.Token
	}

	writeJSON(w, http.StatusOK, response)
}

// handleWeChatLogin handles the mini program's wx.login callback.
// POST /api/v1/auth/wechat/login
// Body: { "code": "wx_login_code", "scene": "A3F8K2M1" }
// The server exchanges the code for openid via WeChat's code2Session API,
// finds or creates a user, and completes the auth session.
func (s *Server) handleWeChatLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, http.StatusMethodNotAllowed, "Only POST method is supported")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}
	defer r.Body.Close()

	var req struct {
		Code  string `json:"code"`  // wx.login() temporary code
		Scene string `json:"scene"` // scene from QR code / pairing code
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON parse failed: "+err.Error())
		return
	}

	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "Missing code parameter (from wx.login)")
		return
	}
	if req.Scene == "" {
		writeError(w, http.StatusBadRequest, "Missing scene parameter")
		return
	}

	// Verify the auth session exists and is still pending
	sess, err := s.store.GetAuthSession(req.Scene)
	if err != nil {
		writeError(w, http.StatusNotFound, "Session not found or expired, please request a new QR code")
		return
	}
	if sess.Status == "expired" || time.Now().Unix() > sess.ExpiresAt {
		writeError(w, http.StatusGone, "Session expired, please request a new QR code")
		return
	}
	if sess.Status == "scanned" {
		// Already scanned — return the existing token
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"token":   sess.Token,
			"message": "Already logged in",
		})
		return
	}

	// Exchange code for openid via WeChat API
	code2SessionResp, err := s.code2Session(req.Code)
	if err != nil {
		log.Printf("WeChat code2Session failed: %v", err)
		writeError(w, http.StatusBadGateway, "WeChat login failed: "+err.Error())
		return
	}

	if code2SessionResp.OpenID == "" {
		writeError(w, http.StatusBadGateway, "WeChat returned empty openid")
		return
	}

	// Find or create user by openid
	token, err := s.store.FindOrCreateUserByOpenID(code2SessionResp.OpenID)
	if err != nil {
		log.Printf("FindOrCreateUser failed: %v", err)
		writeError(w, http.StatusInternalServerError, "User creation failed: "+err.Error())
		return
	}

	// Complete the auth session — bind token to scene so desktop can pick it up
	if err := s.store.CompleteAuthSession(req.Scene, token, code2SessionResp.OpenID); err != nil {
		log.Printf("CompleteAuthSession failed: %v", err)
		writeError(w, http.StatusInternalServerError, "Complete auth session failed: "+err.Error())
		return
	}

	log.Printf("WeChat login success: openid=%s..., scene=%s, token=%s...",
		code2SessionResp.OpenID[:min(8, len(code2SessionResp.OpenID))],
		req.Scene,
		token[:8],
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":   token,
		"message": "Login successful",
	})
}

// handleGetStatus returns the sync status for the authenticated user
// GET /api/v1/user/status
func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, http.StatusMethodNotAllowed, "Only GET method is supported")
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
		writeError(w, http.StatusMethodNotAllowed, "Only POST method is supported")
		return
	}

	token := tokenFromContext(r.Context())

	// Limit request body size (10MB)
	r.Body = http.MaxBytesReader(w, r.Body, 10*1024*1024)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}
	defer r.Body.Close()

	var req SyncPushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON parse failed: "+err.Error())
		return
	}

	if len(req.Entries) == 0 {
		writeError(w, http.StatusBadRequest, "entries cannot be empty")
		return
	}

	if len(req.Entries) > 1000 {
		writeError(w, http.StatusBadRequest, "Max 1000 entries per push")
		return
	}

	upserted, err := s.store.PushEntries(token, req.Entries)
	if err != nil {
		log.Printf("Push failed: %v", err)
		writeError(w, http.StatusInternalServerError, "Push failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"upserted": upserted,
		"total":    len(req.Entries),
		"message":  fmt.Sprintf("Synced %d entries", upserted),
	})
}

// handlePull pulls entries from server to client
// GET /api/v1/sync/pull?since=<timestamp>
// If since is 0 or omitted, returns all non-deleted entries (initial sync)
func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, http.StatusMethodNotAllowed, "Only GET method is supported")
		return
	}

	token := tokenFromContext(r.Context())

	var since int64 = 0
	if s := r.URL.Query().Get("since"); s != "" {
		if _, err := fmt.Sscanf(s, "%d", &since); err != nil {
			writeError(w, http.StatusBadRequest, "since parameter should be a Unix timestamp")
			return
		}
	}

	entries, err := s.store.PullEntries(token, since)
	if err != nil {
		log.Printf("Pull failed: %v", err)
		writeError(w, http.StatusInternalServerError, "Pull failed: "+err.Error())
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
		writeError(w, http.StatusMethodNotAllowed, "Only POST method is supported")
		return
	}

	token := tokenFromContext(r.Context())

	var req struct {
		ID string `json:"id"`
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}
	defer r.Body.Close()

	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON parse failed: "+err.Error())
		return
	}

	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id cannot be empty")
		return
	}

	if err := s.store.DeleteEntry(token, req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Delete failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Deleted",
	})
}

// handleHealth returns server health status
// GET /api/v1/health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"service": "wordwise-sync",
		"version": "1.1.0",
		"time":    time.Now().Unix(),
		"wechat":  s.appID != "",
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

// encodeBase64 returns a base64 encoding of the input bytes.
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
