package main

import (
	"compress/gzip"
	"context"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

//go:embed index.html share.html admin.html profile.html styles.css app.js favicon.png
var staticFiles embed.FS

const (
	sessionCookieName         = "share_session"
	downloadTicketCookieName  = "share_download_ticket_"
	loginIPAttemptPrefix      = "ip:"
	loginAccountAttemptPrefix = "account:"
	loginAttemptTTL           = 15 * time.Minute // Longer than the 5-minute lock window so active locks survive cleanup.
	maxLoginAttemptKeys       = 4096
)

type loginAttempt struct {
	failures    int
	lockedUntil time.Time
	lastSeen    time.Time
}

type downloadTicket struct {
	fileID      int64
	sessionHash string
	expiresAt   time.Time
}

type server struct {
	cfg       config
	db        *database
	logger    *slog.Logger
	now       func() time.Time
	attemptMu sync.Mutex
	attempts  map[string]loginAttempt
	ticketMu  sync.Mutex
	tickets   map[string]downloadTicket
}

func newServer(cfg config, db *database, logger *slog.Logger) *server {
	return &server{
		cfg:      cfg,
		db:       db,
		logger:   logger,
		now:      func() time.Time { return time.Now().UTC().Truncate(time.Second) },
		attempts: make(map[string]loginAttempt),
		tickets:  make(map[string]downloadTicket),
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /index.html", s.handleIndex)
	mux.HandleFunc("GET /share.html", s.handleSharePage)
	mux.HandleFunc("GET /admin.html", s.handleAdminPage)
	mux.HandleFunc("GET /profile.html", s.handleProfilePage)
	mux.HandleFunc("GET /styles.css", s.serveStatic("styles.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /app.js", s.serveStatic("app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /favicon.png", s.serveStatic("favicon.png", "image/png"))
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /api/session", s.withSession(s.handleSession))
	mux.HandleFunc("POST /api/logout", s.withSession(s.withCSRF(s.handleLogout)))
	mux.HandleFunc("POST /api/me/password", s.withSession(s.withCSRF(s.handleChangeOwnPassword)))
	mux.HandleFunc("GET /api/admin/users", s.withAdmin(s.handleAdminUsers))
	mux.HandleFunc("POST /api/admin/users", s.withAdmin(s.withCSRF(s.handleAdminCreateUser)))
	mux.HandleFunc("POST /api/admin/users/{id}/password", s.withAdmin(s.withCSRF(s.handleAdminSetUserPassword)))
	mux.HandleFunc("POST /api/admin/users/{id}/quota", s.withAdmin(s.withCSRF(s.handleAdminSetUserQuota)))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.withAdmin(s.withCSRF(s.handleAdminDeleteUser)))
	mux.HandleFunc("GET /api/items", s.withSession(s.handleItems))
	mux.HandleFunc("POST /api/texts", s.withSession(s.withCSRF(s.handleCreateText)))
	mux.HandleFunc("POST /api/texts/{id}/copy", s.withSession(s.withCSRF(s.handleCopyText)))
	mux.HandleFunc("DELETE /api/texts/{id}", s.withSession(s.withCSRF(s.handleDeleteText)))
	mux.HandleFunc("POST /api/files", s.withSession(s.withCSRF(s.handleUploadFile)))
	mux.HandleFunc("POST /api/files/{id}/download-ticket", s.withSession(s.withCSRF(s.handleDownloadTicket)))
	mux.HandleFunc("GET /api/files/{id}/download", s.withSession(s.handleDownloadFile))
	mux.HandleFunc("GET /api/files/{id}/preview", s.withSession(s.handleFilePreview))
	mux.HandleFunc("DELETE /api/files/{id}", s.withSession(s.withCSRF(s.handleDeleteFile)))
	return s.securityHeaders(s.requestLog(mux))
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "服务暂不可用")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func redirectForRole(role string) string {
	if role == roleAdmin {
		return "/admin.html"
	}
	return "/share.html"
}

type contextKey string

const sessionContextKey contextKey = "session"

func sessionFromContext(ctx context.Context) session {
	return ctx.Value(sessionContextKey).(session)
}

func (s *server) withSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.readSession(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionContextKey, sess)))
	}
}

func (s *server) withCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFromContext(r.Context())
		provided := r.Header.Get("X-CSRF-Token")
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(sess.CSRFToken)) != 1 {
			writeError(w, http.StatusForbidden, "安全校验失败，请刷新页面后重试")
			return
		}
		next(w, r)
	}
}

func (s *server) withAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.withSession(func(w http.ResponseWriter, r *http.Request) {
		if sessionFromContext(r.Context()).Role != roleAdmin {
			writeError(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next(w, r)
	})
}

func (s *server) readSession(r *http.Request) (session, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return session{}, sql.ErrNoRows
	}
	return s.db.getSession(r.Context(), tokenHash(cookie.Value), s.now())
}

func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}

func (s *server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/login" {
			return
		}
		s.logger.Info("request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	if sess, err := s.readSession(r); err == nil {
		http.Redirect(w, r, redirectForRole(sess.Role), http.StatusSeeOther)
		return
	}
	s.serveStatic("index.html", "text/html; charset=utf-8")(w, r)
}

func (s *server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	sess, err := s.readSession(r)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if sess.Role == roleAdmin {
		http.Redirect(w, r, "/admin.html", http.StatusSeeOther)
		return
	}
	s.serveStatic("share.html", "text/html; charset=utf-8")(w, r)
}

func (s *server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	sess, err := s.readSession(r)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if sess.Role != roleAdmin {
		http.Redirect(w, r, "/share.html", http.StatusSeeOther)
		return
	}
	s.serveStatic("admin.html", "text/html; charset=utf-8")(w, r)
}

func (s *server) handleProfilePage(w http.ResponseWriter, r *http.Request) {
	if _, err := s.readSession(r); err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.serveStatic("profile.html", "text/html; charset=utf-8")(w, r)
}

func (s *server) serveStatic(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		content, err := staticFiles.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Add("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			w.Header().Set("Content-Encoding", "gzip")
			writer := gzip.NewWriter(w)
			defer writer.Close()
			_, _ = writer.Write(content)
			return
		}
		_, _ = w.Write(content)
	}
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		part = strings.TrimSpace(part)
		token, parameters, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(token), "gzip") {
			continue
		}
		for _, parameter := range strings.Split(parameters, ";") {
			name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			return err != nil || quality > 0
		}
		return true
	}
	return false
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input, 8<<10); err != nil {
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	ipKey, accountKey := s.loginAttemptKeys(r, input.Username)
	if delay, locked := s.loginDelay(ipKey, accountKey); locked {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(delay.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "登录尝试过多，请稍后再试")
		return
	} else if delay > 0 {
		time.Sleep(delay)
	}

	account, err := s.db.findUser(r.Context(), input.Username)
	passwordOK := false
	if err == nil {
		passwordOK = verifyPassword(account.PasswordHash, input.Password)
	} else {
		consumePasswordVerificationCost(input.Password)
	}
	if err != nil || !passwordOK {
		s.recordLoginFailure(ipKey, accountKey)
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	s.clearLoginFailures(ipKey, accountKey)

	token, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "登录失败")
		return
	}
	csrf, err := randomToken(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "登录失败")
		return
	}
	if err := s.db.createSession(r.Context(), tokenHash(token), account.ID, csrf, simplifyUserAgent(r.UserAgent()), s.clientIP(r), s.now()); err != nil {
		s.logger.Error("create session", "error", err)
		writeError(w, http.StatusInternalServerError, "登录失败")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		Secure:   s.cfg.cookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"username": account.Username, "role": account.Role, "csrfToken": csrf, "redirectTo": redirectForRole(account.Role)})
}

func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"username": sess.Username, "role": sess.Role, "csrfToken": sess.CSRFToken, "redirectTo": redirectForRole(sess.Role)})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r.Context())
	if err := s.db.deleteSession(r.Context(), sess.TokenHash); err != nil {
		s.logger.Error("delete session", "error", err)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, Secure: s.cfg.cookieSecure, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		return
	}
	sess := sessionFromContext(r.Context())
	account, err := s.db.findUserByID(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "登录已失效")
		return
	}
	if !verifyPassword(account.PasswordHash, input.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "当前密码错误")
		return
	}
	if subtle.ConstantTimeCompare([]byte(input.CurrentPassword), []byte(input.NewPassword)) == 1 {
		writeError(w, http.StatusBadRequest, "新密码不能与当前密码相同")
		return
	}
	hash, err := hashPassword(input.NewPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.db.setUserPasswordByID(r.Context(), sess.UserID, hash, s.now()); err != nil {
		s.logger.Error("change own password", "error", err)
		writeError(w, http.StatusInternalServerError, "修改密码失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, Secure: s.cfg.cookieSecure, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleItems(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r.Context())
	now := s.now()
	items, err := s.db.listItems(r.Context(), sess.UserID, now)
	if err != nil {
		s.logger.Error("list items", "error", err)
		writeError(w, http.StatusInternalServerError, "读取共享内容失败")
		return
	}
	quotaBytes, usedBytes, err := userStorageQuotaAndUsage(r.Context(), s.db.db, sess.UserID, now)
	if err != nil {
		s.logger.Error("read user storage usage", "error", err)
		writeError(w, http.StatusInternalServerError, "读取空间用量失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":             items,
		"storageUsedBytes":  usedBytes,
		"storageQuotaBytes": quotaBytes,
	})
}

func (s *server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.db.listAdminUsers(r.Context(), s.now())
	if err != nil {
		s.logger.Error("list admin users", "error", err)
		writeError(w, http.StatusInternalServerError, "读取用户列表失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if err := validateRegularUsername(input.Username); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.db.setUserPassword(r.Context(), input.Username, hash, s.now()); err != nil {
		s.logger.Error("admin create user", "error", err)
		writeError(w, http.StatusInternalServerError, "保存用户失败")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (s *server) handleAdminSetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, err := parsePositiveID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的用户编号")
		return
	}
	target, err := s.db.findUserByID(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "读取用户失败")
		return
	}
	if target.Role == roleAdmin {
		writeError(w, http.StatusBadRequest, "管理员密码请在个人中心修改")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		return
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.db.setUserPasswordByID(r.Context(), id, hash, s.now()); err != nil {
		s.logger.Error("admin set user password", "error", err)
		writeError(w, http.StatusInternalServerError, "修改密码失败")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleAdminSetUserQuota(w http.ResponseWriter, r *http.Request) {
	id, err := parsePositiveID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的用户编号")
		return
	}
	target, err := s.db.findUserByID(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "读取用户失败")
		return
	}
	if target.Role == roleAdmin {
		writeError(w, http.StatusBadRequest, "管理员账号不使用普通用户空间上限")
		return
	}
	var input struct {
		StorageQuotaBytes int64 `json:"storageQuotaBytes"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		return
	}
	if input.StorageQuotaBytes <= 0 || input.StorageQuotaBytes > maxUserStorageQuotaBytes {
		writeError(w, http.StatusBadRequest, "空间上限必须大于 0 且不能超过 100 TB")
		return
	}
	if err := s.db.setUserStorageQuota(r.Context(), id, input.StorageQuotaBytes, s.now()); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		if errors.Is(err, errReservedAdminUsername) {
			writeError(w, http.StatusBadRequest, "管理员账号不使用普通用户空间上限")
			return
		}
		s.logger.Error("admin set user quota", "error", err)
		writeError(w, http.StatusInternalServerError, "修改空间上限失败")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := parsePositiveID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的用户编号")
		return
	}
	if id == sessionFromContext(r.Context()).UserID {
		writeError(w, http.StatusBadRequest, "不能删除当前登录的管理员账号")
		return
	}
	storedNames, err := s.db.deleteUser(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		if errors.Is(err, errReservedAdminUsername) {
			writeError(w, http.StatusBadRequest, "不能删除管理员账号")
			return
		}
		s.logger.Error("admin delete user", "error", err)
		writeError(w, http.StatusInternalServerError, "删除用户失败")
		return
	}
	for _, storedName := range storedNames {
		if err := os.Remove(filepath.Join(s.cfg.uploadDir, storedName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.logger.Error("remove user file", "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleCreateText(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(w, r, &input, 512<<10); err != nil {
		return
	}
	input.Text = strings.TrimSpace(input.Text)
	if input.Text == "" || utf8.RuneCountInString(input.Text) > maxTextRunes {
		writeError(w, http.StatusBadRequest, "文本不能为空且不能超过 100,000 字符")
		return
	}
	sess := sessionFromContext(r.Context())
	id, err := s.db.createText(r.Context(), sess.UserID, input.Text, simplifyUserAgent(r.UserAgent()), s.now())
	if err != nil {
		if errors.Is(err, errUserTextQuotaExceeded) {
			writeError(w, http.StatusRequestEntityTooLarge, "文本存储空间已达上限，请删除旧文本后再试")
			return
		}
		s.logger.Error("create text", "error", err)
		writeError(w, http.StatusInternalServerError, "发送文本失败")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *server) handleCopyText(w http.ResponseWriter, r *http.Request) {
	s.handleEvent(w, r, "text", "copy")
}

func (s *server) handleEvent(w http.ResponseWriter, r *http.Request, kind, eventType string) {
	id, err := parsePositiveID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的内容编号")
		return
	}
	sess := sessionFromContext(r.Context())
	if err := s.db.addEvent(r.Context(), sess.UserID, kind, id, eventType, simplifyUserAgent(r.UserAgent()), s.now()); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "内容不存在或已过期")
			return
		}
		s.logger.Error("add event", "error", err)
		writeError(w, http.StatusInternalServerError, "记录操作失败")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteText(w http.ResponseWriter, r *http.Request) {
	s.handleDeleteItem(w, r, "text")
}

func (s *server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	s.handleDeleteItem(w, r, "file")
}

func (s *server) handleDeleteItem(w http.ResponseWriter, r *http.Request, kind string) {
	id, err := parsePositiveID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的内容编号")
		return
	}
	sess := sessionFromContext(r.Context())
	storedName, err := s.db.deleteItem(r.Context(), sess.UserID, kind, id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "内容不存在")
			return
		}
		s.logger.Error("delete item", "error", err)
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	if storedName != "" {
		if err := os.Remove(filepath.Join(s.cfg.uploadDir, storedName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.logger.Error("remove uploaded file", "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	if err := os.MkdirAll(s.cfg.uploadDir, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "创建上传目录失败")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFileBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的上传请求")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		writeError(w, http.StatusBadRequest, "请选择一个文件")
		return
	}
	defer part.Close()

	originalName := filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
	if originalName == "." || originalName == "" || utf8.RuneCountInString(originalName) > 255 {
		writeError(w, http.StatusBadRequest, "无效的文件名")
		return
	}
	storedName, err := randomID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "上传失败")
		return
	}
	temp, err := os.CreateTemp(s.cfg.uploadDir, ".upload-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "上传失败")
		return
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	written, err := io.Copy(temp, io.LimitReader(part, maxFileBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "文件上传中断")
		return
	}
	if written > maxFileBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "单个文件不能超过 1 GB")
		return
	}
	if err := temp.Sync(); err != nil || temp.Close() != nil {
		writeError(w, http.StatusInternalServerError, "保存文件失败")
		return
	}

	finalPath := filepath.Join(s.cfg.uploadDir, storedName)
	if err := os.Rename(tempPath, finalPath); err != nil {
		writeError(w, http.StatusInternalServerError, "保存文件失败")
		return
	}
	tempPath = finalPath

	mimeType := detectMIMEType(finalPath)
	sess := sessionFromContext(r.Context())
	id, err := s.db.createFile(r.Context(), fileRecord{
		UserID:     sess.UserID,
		StoredName: storedName,
		FileName:   originalName,
		FileSize:   written,
		MIMEType:   mimeType,
	}, simplifyUserAgent(r.UserAgent()), s.now())
	if err != nil {
		if errors.Is(err, errUserFileQuotaExceeded) {
			writeError(w, http.StatusRequestEntityTooLarge, "文件存储空间已达上限，请删除旧文件后再试")
			return
		}
		s.logger.Error("create file metadata", "error", err)
		writeError(w, http.StatusInternalServerError, "保存文件信息失败")
		return
	}
	committed = true
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func detectMIMEType(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer file.Close()
	buffer := make([]byte, 512)
	count, _ := file.Read(buffer)
	return http.DetectContentType(buffer[:count])
}

func (s *server) handleDownloadTicket(w http.ResponseWriter, r *http.Request) {
	id, err := parsePositiveID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文件编号")
		return
	}
	sess := sessionFromContext(r.Context())
	if _, err := s.db.getFile(r.Context(), sess.UserID, id, s.now()); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "文件不存在或已过期")
			return
		}
		writeError(w, http.StatusInternalServerError, "准备下载失败")
		return
	}
	token, err := randomToken(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "准备下载失败")
		return
	}
	now := s.now()
	s.cleanupExpiredTickets(now)
	s.ticketMu.Lock()
	if len(s.tickets) >= maxDownloadTickets {
		s.ticketMu.Unlock()
		writeError(w, http.StatusTooManyRequests, "下载请求过多，请稍后再试")
		return
	}
	s.tickets[token] = downloadTicket{fileID: id, sessionHash: sess.TokenHash, expiresAt: now.Add(time.Minute)}
	s.ticketMu.Unlock()

	downloadPath := fmt.Sprintf("/api/files/%d/download", id)
	http.SetCookie(w, &http.Cookie{
		Name:     downloadTicketCookieName + strconv.FormatInt(id, 10),
		Value:    token,
		Path:     downloadPath,
		MaxAge:   int(time.Minute.Seconds()),
		Secure:   s.cfg.cookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"url": downloadPath})
}

func (s *server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	id, err := parsePositiveID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sess := sessionFromContext(r.Context())
	ticketToken := ""
	if ticketCookie, err := r.Cookie(downloadTicketCookieName + strconv.FormatInt(id, 10)); err == nil {
		ticketToken = ticketCookie.Value
	}
	s.clearDownloadTicketCookie(w, id)
	if !s.consumeTicket(ticketToken, id, sess.TokenHash) {
		writeError(w, http.StatusForbidden, "下载链接无效或已过期")
		return
	}
	record, err := s.db.getFile(r.Context(), sess.UserID, id, s.now())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filepath.Join(s.cfg.uploadDir, record.StoredName))
	if err != nil {
		s.logger.Error("open uploaded file", "error", err, "file_id", id)
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	modTime := time.Time{}
	if info, err := file.Stat(); err == nil {
		modTime = info.ModTime()
	}
	w.Header().Set("Content-Type", record.MIMEType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": record.FileName}))
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("Range") == "" {
		if err := s.db.addEvent(r.Context(), sess.UserID, "file", id, "download", simplifyUserAgent(r.UserAgent()), s.now()); err != nil {
			s.logger.Error("record download", "file_id", id, "error", err)
		}
	}
	http.ServeContent(w, r, record.FileName, modTime, file)
}

func (s *server) handleFilePreview(w http.ResponseWriter, r *http.Request) {
	id, err := parsePositiveID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sess := sessionFromContext(r.Context())
	record, err := s.db.getFile(r.Context(), sess.UserID, id, s.now())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(strings.ToLower(record.MIMEType), "image/") {
		writeError(w, http.StatusUnsupportedMediaType, "仅支持预览图片文件")
		return
	}
	file, err := os.Open(filepath.Join(s.cfg.uploadDir, record.StoredName))
	if err != nil {
		s.logger.Error("open preview file", "error", err, "file_id", id)
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	modTime := time.Time{}
	if info, err := file.Stat(); err == nil {
		modTime = info.ModTime()
	}
	w.Header().Set("Content-Type", record.MIMEType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": record.FileName}))
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Vary", "Cookie")
	http.ServeContent(w, r, record.FileName, modTime, file)
}

func (s *server) consumeTicket(token string, fileID int64, sessionHash string) bool {
	if token == "" {
		return false
	}
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	ticket, ok := s.tickets[token]
	delete(s.tickets, token)
	return ok && ticket.fileID == fileID && ticket.sessionHash == sessionHash && ticket.expiresAt.After(s.now())
}

func (s *server) clearDownloadTicketCookie(w http.ResponseWriter, fileID int64) {
	http.SetCookie(w, &http.Cookie{
		Name:     downloadTicketCookieName + strconv.FormatInt(fileID, 10),
		Path:     fmt.Sprintf("/api/files/%d/download", fileID),
		MaxAge:   -1,
		Secure:   s.cfg.cookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *server) cleanupExpiredTickets(now time.Time) {
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	for token, ticket := range s.tickets {
		if !ticket.expiresAt.After(now) {
			delete(s.tickets, token)
		}
	}
}

func (s *server) cleanupExpiredAttempts(now time.Time) {
	s.attemptMu.Lock()
	defer s.attemptMu.Unlock()
	s.pruneExpiredAttemptsLocked(now)
}

func (s *server) pruneExpiredAttemptsLocked(now time.Time) {
	for key, attempt := range s.attempts {
		if attempt.lockedUntil.After(now) {
			continue
		}
		if now.Sub(attempt.lastSeen) >= loginAttemptTTL {
			delete(s.attempts, key)
		}
	}
}

func (s *server) evictOldestUnlockedAttemptLocked(now time.Time, protected map[string]struct{}) bool {
	var oldestKey string
	var oldestSeen time.Time
	found := false
	for key, attempt := range s.attempts {
		if _, ok := protected[key]; ok {
			continue
		}
		if attempt.lockedUntil.After(now) {
			continue
		}
		if !found || attempt.lastSeen.Before(oldestSeen) {
			oldestKey = key
			oldestSeen = attempt.lastSeen
			found = true
		}
	}
	if !found {
		return false
	}
	delete(s.attempts, oldestKey)
	return true
}

func (s *server) cleanupExpired(ctx context.Context) error {
	now := s.now()
	s.cleanupExpiredTickets(now)
	s.cleanupExpiredAttempts(now)
	names, err := s.db.expiredFileNames(ctx, now)
	if err != nil {
		return err
	}
	if err := s.db.deleteExpired(ctx, now); err != nil {
		return err
	}
	for _, name := range names {
		if err := os.Remove(filepath.Join(s.cfg.uploadDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.logger.Error("remove expired file", "error", err)
		}
	}
	return nil
}

func (s *server) cleanupLoop(ctx context.Context) {
	if err := s.cleanupExpired(ctx); err != nil {
		s.logger.Error("initial cleanup", "error", err)
	}
	ticker := time.NewTicker(s.cfg.cleanupPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.cleanupExpired(ctx); err != nil {
				s.logger.Error("scheduled cleanup", "error", err)
			}
		}
	}
}

func (s *server) clientIP(r *http.Request) string {
	if s.cfg.trustProxyHeaders {
		if value := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(value) != nil {
			return value
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *server) loginAttemptKeys(r *http.Request, username string) (string, string) {
	ip := s.clientIP(r)
	return loginIPAttemptPrefix + ip, loginAccountAttemptPrefix + strings.ToLower(username)
}

func (s *server) loginDelay(keys ...string) (time.Duration, bool) {
	s.attemptMu.Lock()
	defer s.attemptMu.Unlock()

	now := s.now()
	var maxDelay time.Duration
	var lockedFor time.Duration
	for _, key := range keys {
		attempt := s.attempts[key]
		if attempt.lockedUntil.After(now) {
			remaining := attempt.lockedUntil.Sub(now)
			if remaining > lockedFor {
				lockedFor = remaining
			}
			continue
		}
		if attempt.failures <= 1 {
			continue
		}
		delay := time.Duration(attempt.failures-1) * 200 * time.Millisecond
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		if delay > maxDelay {
			maxDelay = delay
		}
	}
	if lockedFor > 0 {
		return lockedFor, true
	}
	return maxDelay, false
}

func (s *server) recordLoginFailure(keys ...string) {
	s.attemptMu.Lock()
	defer s.attemptMu.Unlock()
	now := s.now()
	protected := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		protected[key] = struct{}{}
	}
	for _, key := range keys {
		attempt, exists := s.attempts[key]
		if !exists && len(s.attempts) >= maxLoginAttemptKeys {
			s.pruneExpiredAttemptsLocked(now)
			if len(s.attempts) >= maxLoginAttemptKeys && !s.evictOldestUnlockedAttemptLocked(now, protected) {
				continue
			}
		}
		attempt.failures++
		attempt.lastSeen = now
		if attempt.failures >= 5 {
			attempt.lockedUntil = now.Add(5 * time.Minute)
		}
		s.attempts[key] = attempt
	}
}

func (s *server) clearLoginFailures(keys ...string) {
	s.attemptMu.Lock()
	for _, key := range keys {
		delete(s.attempts, key)
	}
	s.attemptMu.Unlock()
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any, maxBytes int64) error {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "请求 Content-Type 必须是 application/json")
		if err != nil {
			return err
		}
		return errors.New("unsupported JSON content type")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不正确")
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
