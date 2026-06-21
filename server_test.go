package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type testApp struct {
	server  *server
	handler http.Handler
	db      *database
	userID  int64
}

type loginResult struct {
	Cookie     *http.Cookie
	CSRFToken  string
	Role       string
	RedirectTo string
}

func newTestApp(t *testing.T) testApp {
	t.Helper()
	directory := t.TempDir()
	cfg := config{
		databasePath:  filepath.Join(directory, "share.db"),
		uploadDir:     filepath.Join(directory, "uploads"),
		cookieSecure:  false,
		cleanupPeriod: time.Hour,
	}
	db, err := openDatabase(cfg.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.close() })
	hash, err := hashPassword("Prototype#2026")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 13, 8, 30, 0, 0, time.UTC)
	if err := db.setUserPassword(context.Background(), "demo", hash, now); err != nil {
		t.Fatal(err)
	}
	account, err := db.findUser(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	app := newServer(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	app.now = func() time.Time { return now }
	return testApp{server: app, handler: app.routes(), db: db, userID: account.ID}
}

func (app testApp) login(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	result := app.loginAs(t, "demo", "Prototype#2026")
	return result.Cookie, result.CSRFToken
}

func (app testApp) loginAs(t *testing.T, username, password string) loginResult {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewReader(payload)
	request := httptest.NewRequest(http.MethodPost, "/api/login", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body.String())
	}
	result := struct {
		CSRFToken  string `json:"csrfToken"`
		Role       string `json:"role"`
		RedirectTo string `json:"redirectTo"`
	}{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %d, want 1", len(cookies))
	}
	return loginResult{Cookie: cookies[0], CSRFToken: result.CSRFToken, Role: result.Role, RedirectTo: result.RedirectTo}
}

func performRequest(handler http.Handler, method, path string, body io.Reader, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if body != nil && !strings.Contains(path, "/api/files") {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func requestDownloadTicket(t *testing.T, app testApp, fileID int64, cookie *http.Cookie, csrf string) (string, *http.Cookie) {
	t.Helper()
	ticketResponse := performRequest(app.handler, http.MethodPost, "/api/files/"+strconv.FormatInt(fileID, 10)+"/download-ticket", nil, cookie, csrf)
	if ticketResponse.Code != http.StatusOK {
		t.Fatalf("ticket status = %d, body = %s", ticketResponse.Code, ticketResponse.Body.String())
	}
	var ticket struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(ticketResponse.Body.Bytes(), &ticket); err != nil {
		t.Fatal(err)
	}
	ticketCookies := ticketResponse.Result().Cookies()
	if len(ticketCookies) != 1 || !ticketCookies[0].HttpOnly {
		t.Fatalf("download ticket cookies = %#v, want one HttpOnly cookie", ticketCookies)
	}
	return ticket.URL, ticketCookies[0]
}

func uploadTestFile(t *testing.T, app testApp, cookie *http.Cookie, csrf, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/files", &body)
	request.AddCookie(cookie)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)
	return response
}

func TestReservedAdminAccountRole(t *testing.T) {
	directory := t.TempDir()
	db, err := openDatabase(filepath.Join(directory, "share.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.close() })

	hash, err := hashPassword("Prototype#2026")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 18, 9, 0, 0, 0, time.UTC)
	if err := db.setUserPassword(context.Background(), "admin", hash, now); !errors.Is(err, errReservedAdminUsername) {
		t.Fatalf("set regular admin error = %v, want %v", err, errReservedAdminUsername)
	}
	if err := db.setAdminPassword(context.Background(), hash, now); err != nil {
		t.Fatal(err)
	}
	admin, err := db.findUser(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if admin.Role != roleAdmin {
		t.Fatalf("admin role = %q, want %q", admin.Role, roleAdmin)
	}

	if err := db.setUserPassword(context.Background(), "demo", hash, now); err != nil {
		t.Fatal(err)
	}
	demo, err := db.findUser(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if demo.Role != roleUser {
		t.Fatalf("demo role = %q, want %q", demo.Role, roleUser)
	}

	if err := db.createSession(context.Background(), "admin-session", admin.ID, "csrf", "test device", "127.0.0.1", now); err != nil {
		t.Fatal(err)
	}
	session, err := db.getSession(context.Background(), "admin-session", now)
	if err != nil {
		t.Fatal(err)
	}
	if session.Role != roleAdmin {
		t.Fatalf("session role = %q, want %q", session.Role, roleAdmin)
	}
}

func TestUserCommandReservesAdminName(t *testing.T) {
	directory := t.TempDir()
	db, err := openDatabase(filepath.Join(directory, "share.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.close() })
	t.Setenv("APP_ADMIN_PASSWORD", "Prototype#2026")

	if err := runUserCommand(db, []string{"set-password", "admin"}); !errors.Is(err, errReservedAdminUsername) {
		t.Fatalf("regular admin command error = %v, want %v", err, errReservedAdminUsername)
	}
	if err := runUserCommand(db, []string{"set-admin-password"}); err != nil {
		t.Fatal(err)
	}
	admin, err := db.findUser(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if admin.Role != roleAdmin {
		t.Fatalf("admin role = %q, want %q", admin.Role, roleAdmin)
	}
}

func TestAdminUserManagement(t *testing.T) {
	app := newTestApp(t)
	adminHash, err := hashPassword("AdminPassword#2026")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.db.setAdminPassword(context.Background(), adminHash, app.server.now()); err != nil {
		t.Fatal(err)
	}

	adminLogin := app.loginAs(t, "admin", "AdminPassword#2026")
	if adminLogin.Role != roleAdmin || adminLogin.RedirectTo != "/admin.html" {
		t.Fatalf("admin login role=%q redirect=%q", adminLogin.Role, adminLogin.RedirectTo)
	}
	adminPage := performRequest(app.handler, http.MethodGet, "/admin.html", nil, adminLogin.Cookie, "")
	if adminPage.Code != http.StatusOK {
		t.Fatalf("admin page status = %d", adminPage.Code)
	}
	adminShare := performRequest(app.handler, http.MethodGet, "/share.html", nil, adminLogin.Cookie, "")
	if adminShare.Code != http.StatusSeeOther || adminShare.Header().Get("Location") != "/admin.html" {
		t.Fatalf("admin share redirect = %d %q", adminShare.Code, adminShare.Header().Get("Location"))
	}

	userLogin := app.loginAs(t, "demo", "Prototype#2026")
	userAdminAPI := performRequest(app.handler, http.MethodGet, "/api/admin/users", nil, userLogin.Cookie, "")
	if userAdminAPI.Code != http.StatusForbidden {
		t.Fatalf("user admin API status = %d", userAdminAPI.Code)
	}

	createReserved := performRequest(app.handler, http.MethodPost, "/api/admin/users", strings.NewReader(`{"username":"admin","password":"AnotherPassword#2026"}`), adminLogin.Cookie, adminLogin.CSRFToken)
	if createReserved.Code != http.StatusBadRequest {
		t.Fatalf("create reserved status = %d", createReserved.Code)
	}
	create := performRequest(app.handler, http.MethodPost, "/api/admin/users", strings.NewReader(`{"username":"alice","password":"AlicePassword#2026"}`), adminLogin.Cookie, adminLogin.CSRFToken)
	if create.Code != http.StatusCreated {
		t.Fatalf("create user status = %d, body = %s", create.Code, create.Body.String())
	}
	aliceLogin := app.loginAs(t, "alice", "AlicePassword#2026")
	createText := performRequest(app.handler, http.MethodPost, "/api/texts", strings.NewReader(`{"text":"hello from alice"}`), aliceLogin.Cookie, aliceLogin.CSRFToken)
	if createText.Code != http.StatusCreated {
		t.Fatalf("alice create text status = %d, body = %s", createText.Code, createText.Body.String())
	}

	list := performRequest(app.handler, http.MethodGet, "/api/admin/users", nil, adminLogin.Cookie, "")
	if list.Code != http.StatusOK {
		t.Fatalf("admin list status = %d, body = %s", list.Code, list.Body.String())
	}
	var listed struct {
		Users []adminUserRecord `json:"users"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	var alice adminUserRecord
	for _, user := range listed.Users {
		if user.Username == "alice" {
			alice = user
			break
		}
	}
	if alice.ID == 0 || alice.Role != roleUser || alice.LastLoginAt == nil || alice.LastUploadAt == nil || alice.TextCount != 1 {
		t.Fatalf("alice admin record = %#v", alice)
	}

	reset := performRequest(app.handler, http.MethodPost, "/api/admin/users/"+strconv.FormatInt(alice.ID, 10)+"/password", strings.NewReader(`{"password":"AliceNewPassword#2026"}`), adminLogin.Cookie, adminLogin.CSRFToken)
	if reset.Code != http.StatusNoContent {
		t.Fatalf("reset password status = %d, body = %s", reset.Code, reset.Body.String())
	}
	oldLogin := performRequest(app.handler, http.MethodPost, "/api/login", strings.NewReader(`{"username":"alice","password":"AlicePassword#2026"}`), nil, "")
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("old alice login status = %d", oldLogin.Code)
	}
	newLogin := performRequest(app.handler, http.MethodPost, "/api/login", strings.NewReader(`{"username":"alice","password":"AliceNewPassword#2026"}`), nil, "")
	if newLogin.Code != http.StatusOK {
		t.Fatalf("new alice login status = %d, body = %s", newLogin.Code, newLogin.Body.String())
	}

	deleteAdmin := performRequest(app.handler, http.MethodDelete, "/api/admin/users/999999", nil, adminLogin.Cookie, adminLogin.CSRFToken)
	if deleteAdmin.Code != http.StatusNotFound {
		t.Fatalf("delete missing user status = %d", deleteAdmin.Code)
	}
	deleted := performRequest(app.handler, http.MethodDelete, "/api/admin/users/"+strconv.FormatInt(alice.ID, 10), nil, adminLogin.Cookie, adminLogin.CSRFToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete alice status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	afterDelete := performRequest(app.handler, http.MethodPost, "/api/login", strings.NewReader(`{"username":"alice","password":"AliceNewPassword#2026"}`), nil, "")
	if afterDelete.Code != http.StatusUnauthorized {
		t.Fatalf("deleted alice login status = %d", afterDelete.Code)
	}
}

func TestDefaultStorageQuotaAndUsageInAdminList(t *testing.T) {
	app := newTestApp(t)
	now := app.server.now()
	if _, err := app.db.createText(context.Background(), app.userID, "hello", "test device", now); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.createFile(context.Background(), fileRecord{
		UserID:     app.userID,
		StoredName: "stored-usage-file",
		FileName:   "usage.bin",
		FileSize:   11,
		MIMEType:   "application/octet-stream",
	}, "test device", now); err != nil {
		t.Fatal(err)
	}

	users, err := app.db.listAdminUsers(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	var demo adminUserRecord
	for _, user := range users {
		if user.Username == "demo" {
			demo = user
			break
		}
	}
	if demo.ID == 0 {
		t.Fatal("demo user not listed")
	}
	if demo.StorageQuotaBytes != defaultUserStorageQuotaBytes {
		t.Fatalf("demo quota = %d, want %d", demo.StorageQuotaBytes, defaultUserStorageQuotaBytes)
	}
	if demo.StorageUsedBytes != int64(len("hello"))+11 {
		t.Fatalf("demo usage = %d, want %d", demo.StorageUsedBytes, int64(len("hello"))+11)
	}
}

func TestAdminCanAdjustUserStorageQuota(t *testing.T) {
	app := newTestApp(t)
	adminHash, err := hashPassword("AdminPassword#2026")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.db.setAdminPassword(context.Background(), adminHash, app.server.now()); err != nil {
		t.Fatal(err)
	}
	adminLogin := app.loginAs(t, "admin", "AdminPassword#2026")

	create := performRequest(app.handler, http.MethodPost, "/api/admin/users", strings.NewReader(`{"username":"quotauser","password":"QuotaPassword#2026"}`), adminLogin.Cookie, adminLogin.CSRFToken)
	if create.Code != http.StatusCreated {
		t.Fatalf("create user status = %d, body = %s", create.Code, create.Body.String())
	}
	target, err := app.db.findUser(context.Background(), "quotauser")
	if err != nil {
		t.Fatal(err)
	}
	list := performRequest(app.handler, http.MethodGet, "/api/admin/users", nil, adminLogin.Cookie, "")
	if list.Code != http.StatusOK {
		t.Fatalf("admin list status = %d, body = %s", list.Code, list.Body.String())
	}
	var listed struct {
		Users []adminUserRecord `json:"users"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	var quotaUser adminUserRecord
	for _, user := range listed.Users {
		if user.Username == "quotauser" {
			quotaUser = user
			break
		}
	}
	if quotaUser.StorageQuotaBytes != defaultUserStorageQuotaBytes {
		t.Fatalf("new user quota = %d, want %d", quotaUser.StorageQuotaBytes, defaultUserStorageQuotaBytes)
	}

	update := performRequest(
		app.handler,
		http.MethodPost,
		"/api/admin/users/"+strconv.FormatInt(target.ID, 10)+"/quota",
		strings.NewReader(`{"storageQuotaBytes":10737418240}`),
		adminLogin.Cookie,
		adminLogin.CSRFToken,
	)
	if update.Code != http.StatusNoContent {
		t.Fatalf("quota update status = %d, body = %s", update.Code, update.Body.String())
	}
	updated, err := app.db.findUserByID(context.Background(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.StorageQuotaBytes != 10*1024*1024*1024 {
		t.Fatalf("updated quota = %d, want 10GiB", updated.StorageQuotaBytes)
	}
}

func TestLoginRateLimitBlocksRotatingUsernamesFromSameIP(t *testing.T) {
	app := newTestApp(t)

	for attempt := 1; attempt <= 6; attempt++ {
		payload, err := json.Marshal(map[string]string{
			"username": "ghost-user-" + strconv.Itoa(attempt),
			"password": "WrongPassword#2026",
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "203.0.113.10:49152"
		response := httptest.NewRecorder()
		app.handler.ServeHTTP(response, request)

		if attempt < 6 && response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.Code, http.StatusUnauthorized)
		}
		if attempt == 6 && response.Code != http.StatusTooManyRequests {
			t.Fatalf("rotating username attempt status = %d, want %d", response.Code, http.StatusTooManyRequests)
		}
	}
}

func TestLoginRejectsNonJSONContentType(t *testing.T) {
	app := newTestApp(t)
	request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"demo","password":"Prototype#2026"}`))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-JSON login status = %d, want %d", response.Code, http.StatusUnsupportedMediaType)
	}
}

func TestLoginRateLimitBlocksSameAccountAcrossIPs(t *testing.T) {
	app := newTestApp(t)

	for attempt := 1; attempt <= 6; attempt++ {
		payload, err := json.Marshal(map[string]string{
			"username": "demo",
			"password": "WrongPassword#2026",
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "198.51.100." + strconv.Itoa(attempt) + ":49152"
		response := httptest.NewRecorder()
		app.handler.ServeHTTP(response, request)

		if attempt < 6 && response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.Code, http.StatusUnauthorized)
		}
		if attempt == 6 && response.Code != http.StatusTooManyRequests {
			t.Fatalf("cross-IP same account status = %d, want %d", response.Code, http.StatusTooManyRequests)
		}
	}
}

func TestLoginAttemptsEvictOldestUnlockedKeysAtGlobalCap(t *testing.T) {
	app := newTestApp(t)
	now := app.server.now()

	app.server.attemptMu.Lock()
	for index := 0; index < maxLoginAttemptKeys; index++ {
		app.server.attempts["seed:"+strconv.Itoa(index)] = loginAttempt{failures: 1, lastSeen: now}
	}
	app.server.attempts["seed:0"] = loginAttempt{failures: 1, lastSeen: now.Add(-2 * time.Minute)}
	app.server.attempts["seed:1"] = loginAttempt{failures: 1, lastSeen: now.Add(-1 * time.Minute)}
	app.server.attemptMu.Unlock()

	username := "ghost-user"
	payload, err := json.Marshal(map[string]string{
		"username": username,
		"password": "WrongPassword#2026",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "203.0.113.250:49152"
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("login status at attempt cap = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	app.server.attemptMu.Lock()
	attemptCount := len(app.server.attempts)
	_, oldestExists := app.server.attempts["seed:0"]
	_, secondOldestExists := app.server.attempts["seed:1"]
	_, ipAttemptExists := app.server.attempts[loginIPAttemptPrefix+"203.0.113.250"]
	_, accountAttemptExists := app.server.attempts[loginAccountAttemptPrefix+username]
	app.server.attemptMu.Unlock()
	if attemptCount != maxLoginAttemptKeys {
		t.Fatalf("login attempt keys = %d, want %d", attemptCount, maxLoginAttemptKeys)
	}
	if oldestExists || secondOldestExists {
		t.Fatal("oldest unlocked login attempt keys were not evicted")
	}
	if !ipAttemptExists || !accountAttemptExists {
		t.Fatal("new login attempt keys were not recorded at the global cap")
	}
}

func TestLoginAttemptEvictionKeepsLockedKeys(t *testing.T) {
	app := newTestApp(t)
	now := app.server.now()
	lockedKey := loginIPAttemptPrefix + "198.51.100.250"
	newKey := loginIPAttemptPrefix + "203.0.113.251"

	app.server.attemptMu.Lock()
	app.server.attempts[lockedKey] = loginAttempt{
		failures:    5,
		lastSeen:    now.Add(-10 * time.Minute),
		lockedUntil: now.Add(4 * time.Minute),
	}
	for index := 1; index < maxLoginAttemptKeys; index++ {
		app.server.attempts["seed:"+strconv.Itoa(index)] = loginAttempt{
			failures: 1,
			lastSeen: now.Add(-time.Duration(index) * time.Millisecond),
		}
	}
	app.server.attemptMu.Unlock()

	app.server.recordLoginFailure(newKey)

	app.server.attemptMu.Lock()
	attemptCount := len(app.server.attempts)
	_, lockedExists := app.server.attempts[lockedKey]
	_, newExists := app.server.attempts[newKey]
	app.server.attemptMu.Unlock()
	if attemptCount != maxLoginAttemptKeys {
		t.Fatalf("login attempt keys = %d, want %d", attemptCount, maxLoginAttemptKeys)
	}
	if !lockedExists {
		t.Fatal("locked login attempt key was evicted")
	}
	if !newExists {
		t.Fatal("new login attempt key was not recorded")
	}
}

func TestCleanupExpiredRemovesStaleLoginAttempts(t *testing.T) {
	app := newTestApp(t)
	current := app.server.now()
	app.server.now = func() time.Time { return current }
	key := loginIPAttemptPrefix + "198.51.100.20"

	app.server.recordLoginFailure(key)
	app.server.recordLoginFailure(key)

	app.server.attemptMu.Lock()
	attemptCount := len(app.server.attempts)
	app.server.attemptMu.Unlock()
	if attemptCount != 1 {
		t.Fatalf("login attempt count = %d, want 1", attemptCount)
	}

	current = current.Add(16 * time.Minute)
	if err := app.server.cleanupExpired(context.Background()); err != nil {
		t.Fatal(err)
	}

	app.server.attemptMu.Lock()
	attemptCount = len(app.server.attempts)
	app.server.attemptMu.Unlock()
	if attemptCount != 0 {
		t.Fatalf("stale login attempt count = %d, want 0", attemptCount)
	}
}

func TestCleanupExpiredKeepsLockedLoginAttempts(t *testing.T) {
	app := newTestApp(t)
	current := app.server.now()
	app.server.now = func() time.Time { return current }
	key := loginIPAttemptPrefix + "203.0.113.20"

	for attempt := 0; attempt < 5; attempt++ {
		app.server.recordLoginFailure(key)
	}

	current = current.Add(time.Minute)
	if err := app.server.cleanupExpired(context.Background()); err != nil {
		t.Fatal(err)
	}

	app.server.attemptMu.Lock()
	stored, ok := app.server.attempts[key]
	app.server.attemptMu.Unlock()
	if !ok {
		t.Fatal("locked login attempt was removed")
	}
	if !stored.lockedUntil.After(current) {
		t.Fatalf("lockedUntil = %s, want after %s", stored.lockedUntil, current)
	}
}

func TestClientIPIgnoresProxyHeaderByDefault(t *testing.T) {
	srv := newServer(config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.1:5000"
	request.Header.Set("X-Real-IP", "1.2.3.4")

	if got := srv.clientIP(request); got != "10.0.0.1" {
		t.Fatalf("clientIP = %q, want RemoteAddr host", got)
	}
}

func TestClientIPTrustsProxyHeaderWhenConfigured(t *testing.T) {
	srv := newServer(config{trustProxyHeaders: true}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.1:5000"
	request.Header.Set("X-Real-IP", "1.2.3.4")

	if got := srv.clientIP(request); got != "1.2.3.4" {
		t.Fatalf("clientIP = %q, want X-Real-IP", got)
	}
}

func TestPasswordChangeRevokesExistingSession(t *testing.T) {
	app := newTestApp(t)
	cookie, _ := app.login(t)

	newHash, err := hashPassword("UpdatedPassword#2026")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.db.setUserPassword(context.Background(), "demo", newHash, app.server.now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	response := performRequest(app.handler, http.MethodGet, "/api/session", nil, cookie, "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("session after password change status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	body := strings.NewReader(`{"username":"demo","password":"UpdatedPassword#2026"}`)
	login := performRequest(app.handler, http.MethodPost, "/api/login", body, nil, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login with updated password status = %d, body = %s", login.Code, login.Body.String())
	}
}

func TestPasswordChangeRollsBackWhenSessionRevocationFails(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.db.db.Exec(`
		CREATE TRIGGER deny_session_delete
		BEFORE DELETE ON sessions
		BEGIN
			SELECT RAISE(FAIL, 'deny session delete');
		END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.db.Exec(`
		INSERT INTO sessions(token_hash, user_id, csrf_token, device_label, ip_address, created_at, expires_at)
		VALUES('session-to-revoke', ?, 'csrf', 'test device', '127.0.0.1', ?, ?)
	`, app.userID, app.server.now().Unix(), app.server.now().Add(sessionTTL).Unix()); err != nil {
		t.Fatal(err)
	}

	newHash, err := hashPassword("UpdatedPassword#2026")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.db.setUserPasswordByID(context.Background(), app.userID, newHash, app.server.now().Add(time.Minute)); err == nil {
		t.Fatal("setUserPasswordByID error = nil, want trigger failure")
	}
	account, err := app.db.findUserByID(context.Background(), app.userID)
	if err != nil {
		t.Fatal(err)
	}
	if verifyPassword(account.PasswordHash, "UpdatedPassword#2026") {
		t.Fatal("password update survived failed session revocation")
	}
	if !verifyPassword(account.PasswordHash, "Prototype#2026") {
		t.Fatal("original password was not preserved after rollback")
	}
}

func TestSelfPasswordChangeRevokesSession(t *testing.T) {
	app := newTestApp(t)
	login := app.loginAs(t, "demo", "Prototype#2026")

	profilePage := performRequest(app.handler, http.MethodGet, "/profile.html", nil, login.Cookie, "")
	if profilePage.Code != http.StatusOK {
		t.Fatalf("profile page status = %d", profilePage.Code)
	}
	wrongCurrent := performRequest(app.handler, http.MethodPost, "/api/me/password", strings.NewReader(`{"currentPassword":"WrongPassword#2026","newPassword":"UpdatedPassword#2026"}`), login.Cookie, login.CSRFToken)
	if wrongCurrent.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password status = %d", wrongCurrent.Code)
	}
	samePassword := performRequest(app.handler, http.MethodPost, "/api/me/password", strings.NewReader(`{"currentPassword":"Prototype#2026","newPassword":"Prototype#2026"}`), login.Cookie, login.CSRFToken)
	if samePassword.Code != http.StatusBadRequest {
		t.Fatalf("same password status = %d, body = %s", samePassword.Code, samePassword.Body.String())
	}
	if !strings.Contains(samePassword.Body.String(), "新密码不能与当前密码相同") {
		t.Fatalf("same password response = %s", samePassword.Body.String())
	}
	changed := performRequest(app.handler, http.MethodPost, "/api/me/password", strings.NewReader(`{"currentPassword":"Prototype#2026","newPassword":"UpdatedPassword#2026"}`), login.Cookie, login.CSRFToken)
	if changed.Code != http.StatusNoContent {
		t.Fatalf("change password status = %d, body = %s", changed.Code, changed.Body.String())
	}
	if len(changed.Result().Cookies()) != 1 || changed.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("change password cookies = %#v", changed.Result().Cookies())
	}
	sessionAfterChange := performRequest(app.handler, http.MethodGet, "/api/session", nil, login.Cookie, "")
	if sessionAfterChange.Code != http.StatusUnauthorized {
		t.Fatalf("session after self password change status = %d", sessionAfterChange.Code)
	}
	oldLogin := performRequest(app.handler, http.MethodPost, "/api/login", strings.NewReader(`{"username":"demo","password":"Prototype#2026"}`), nil, "")
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("old password login status = %d", oldLogin.Code)
	}
	newLogin := performRequest(app.handler, http.MethodPost, "/api/login", strings.NewReader(`{"username":"demo","password":"UpdatedPassword#2026"}`), nil, "")
	if newLogin.Code != http.StatusOK {
		t.Fatalf("new password login status = %d, body = %s", newLogin.Code, newLogin.Body.String())
	}
}

func TestAuthenticationAndTextLifecycle(t *testing.T) {
	app := newTestApp(t)

	unauthorized := performRequest(app.handler, http.MethodGet, "/api/items", nil, nil, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	cookie, csrf := app.login(t)
	withoutCSRF := performRequest(app.handler, http.MethodPost, "/api/texts", strings.NewReader(`{"text":"hello"}`), cookie, "")
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", withoutCSRF.Code)
	}

	created := performRequest(app.handler, http.MethodPost, "/api/texts", strings.NewReader(`{"text":"hello from phone"}`), cookie, csrf)
	if created.Code != http.StatusCreated {
		t.Fatalf("create text status = %d, body = %s", created.Code, created.Body.String())
	}
	var createResult struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createResult); err != nil {
		t.Fatal(err)
	}

	copied := performRequest(app.handler, http.MethodPost, "/api/texts/1/copy", nil, cookie, csrf)
	if copied.Code != http.StatusNoContent {
		t.Fatalf("copy status = %d, body = %s", copied.Code, copied.Body.String())
	}
	listed := performRequest(app.handler, http.MethodGet, "/api/items", nil, cookie, "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listed.Code, listed.Body.String())
	}
	var listResult struct {
		Items             []itemRecord `json:"items"`
		StorageUsedBytes  int64        `json:"storageUsedBytes"`
		StorageQuotaBytes int64        `json:"storageQuotaBytes"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listResult); err != nil {
		t.Fatal(err)
	}
	if len(listResult.Items) != 1 || len(listResult.Items[0].Events) != 1 {
		t.Fatalf("listed items = %#v", listResult.Items)
	}
	if listResult.StorageUsedBytes != int64(len("hello from phone")) || listResult.StorageQuotaBytes != defaultUserStorageQuotaBytes {
		t.Fatalf("storage result = used %d quota %d", listResult.StorageUsedBytes, listResult.StorageQuotaBytes)
	}

	deleted := performRequest(app.handler, http.MethodDelete, "/api/texts/1", nil, cookie, csrf)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", deleted.Code)
	}
	if createResult.ID != 1 {
		t.Fatalf("created ID = %d, want 1", createResult.ID)
	}
}

func TestItemsStorageUsageRestoresAfterFileDelete(t *testing.T) {
	app := newTestApp(t)
	cookie, csrf := app.login(t)
	content := []byte("file quota lifecycle")

	uploaded := uploadTestFile(t, app, cookie, csrf, "quota-lifecycle.txt", content)
	if uploaded.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", uploaded.Code, uploaded.Body.String())
	}
	var uploadResult struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(uploaded.Body.Bytes(), &uploadResult); err != nil {
		t.Fatal(err)
	}
	afterUpload := performRequest(app.handler, http.MethodGet, "/api/items", nil, cookie, "")
	if afterUpload.Code != http.StatusOK {
		t.Fatalf("list after upload status = %d, body = %s", afterUpload.Code, afterUpload.Body.String())
	}
	var uploadList struct {
		StorageUsedBytes  int64 `json:"storageUsedBytes"`
		StorageQuotaBytes int64 `json:"storageQuotaBytes"`
	}
	if err := json.Unmarshal(afterUpload.Body.Bytes(), &uploadList); err != nil {
		t.Fatal(err)
	}
	if uploadList.StorageUsedBytes != int64(len(content)) || uploadList.StorageQuotaBytes != defaultUserStorageQuotaBytes {
		t.Fatalf("storage after upload = used %d quota %d", uploadList.StorageUsedBytes, uploadList.StorageQuotaBytes)
	}

	deleted := performRequest(app.handler, http.MethodDelete, "/api/files/"+strconv.FormatInt(uploadResult.ID, 10), nil, cookie, csrf)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	afterDelete := performRequest(app.handler, http.MethodGet, "/api/items", nil, cookie, "")
	if afterDelete.Code != http.StatusOK {
		t.Fatalf("list after delete status = %d, body = %s", afterDelete.Code, afterDelete.Body.String())
	}
	var deleteList struct {
		StorageUsedBytes  int64 `json:"storageUsedBytes"`
		StorageQuotaBytes int64 `json:"storageQuotaBytes"`
	}
	if err := json.Unmarshal(afterDelete.Body.Bytes(), &deleteList); err != nil {
		t.Fatal(err)
	}
	if deleteList.StorageUsedBytes != 0 || deleteList.StorageQuotaBytes != defaultUserStorageQuotaBytes {
		t.Fatalf("storage after delete = used %d quota %d", deleteList.StorageUsedBytes, deleteList.StorageQuotaBytes)
	}
}

func TestCreateTextRejectsUserQuotaExceeded(t *testing.T) {
	app := newTestApp(t)
	cookie, csrf := app.login(t)
	now := app.server.now()
	if err := app.db.setUserStorageQuota(context.Background(), app.userID, 5, now); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.createFile(context.Background(), fileRecord{
		UserID:     app.userID,
		StoredName: "existing-quota-file",
		FileName:   "existing.bin",
		FileSize:   4,
		MIMEType:   "application/octet-stream",
	}, "test device", now); err != nil {
		t.Fatal(err)
	}

	created := performRequest(app.handler, http.MethodPost, "/api/texts", strings.NewReader(`{"text":"hi"}`), cookie, csrf)
	if created.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("create text over quota status = %d, want %d; body = %s", created.Code, http.StatusRequestEntityTooLarge, created.Body.String())
	}
	items, err := app.db.listItems(context.Background(), app.userID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items after rejected text = %d, want 1", len(items))
	}
}

func TestCopyEventsAreCappedPerItem(t *testing.T) {
	app := newTestApp(t)
	cookie, csrf := app.login(t)

	created := performRequest(app.handler, http.MethodPost, "/api/texts", strings.NewReader(`{"text":"hello"}`), cookie, csrf)
	if created.Code != http.StatusCreated {
		t.Fatalf("create text status = %d, body = %s", created.Code, created.Body.String())
	}
	for index := 0; index < maxItemEvents+1; index++ {
		copied := performRequest(app.handler, http.MethodPost, "/api/texts/1/copy", nil, cookie, csrf)
		if copied.Code != http.StatusNoContent {
			t.Fatalf("copy %d status = %d, body = %s", index, copied.Code, copied.Body.String())
		}
	}
	items, err := app.db.listItems(context.Background(), app.userID, app.server.now())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Events) != maxItemEvents {
		t.Fatalf("event count = %#v, want %d events", items, maxItemEvents)
	}
}

func TestHealthAndSecurityHeaders(t *testing.T) {
	app := newTestApp(t)
	response := performRequest(app.handler, http.MethodGet, "/healthz", nil, nil, "")
	if response.Code != http.StatusOK || response.Body.String() != "ok\n" {
		t.Fatalf("health response = %d %q", response.Code, response.Body.String())
	}
	for _, header := range []string{
		"Content-Security-Policy",
		"Strict-Transport-Security",
		"X-Content-Type-Options",
		"X-Frame-Options",
	} {
		if response.Header().Get(header) == "" {
			t.Fatalf("security header %s is missing", header)
		}
	}
}

func TestStaticAssetsUseGzipWhenAccepted(t *testing.T) {
	app := newTestApp(t)
	for _, acceptEncoding := range []string{"gzip", "br, gzip;q=0.5"} {
		for _, path := range []string{"/", "/styles.css", "/app.js", "/favicon.png"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Accept-Encoding", acceptEncoding)
			response := httptest.NewRecorder()
			app.handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("%s status = %d, body = %s", path, response.Code, response.Body.String())
			}
			if encoding := response.Header().Get("Content-Encoding"); encoding != "gzip" {
				t.Fatalf("%s with %q Content-Encoding = %q, want gzip", path, acceptEncoding, encoding)
			}
			if vary := response.Header().Values("Vary"); !slices.Contains(vary, "Accept-Encoding") {
				t.Fatalf("%s Vary = %#v, want Accept-Encoding", path, vary)
			}
			reader, err := gzip.NewReader(bytes.NewReader(response.Body.Bytes()))
			if err != nil {
				t.Fatalf("%s gzip reader: %v", path, err)
			}
			body, err := io.ReadAll(reader)
			if closeErr := reader.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				t.Fatalf("%s gzip read: %v", path, err)
			}
			if len(body) == 0 {
				t.Fatalf("%s gzip body is empty", path)
			}
		}
	}
}

func TestFaviconServesPNG(t *testing.T) {
	app := newTestApp(t)
	response := performRequest(app.handler, http.MethodGet, "/favicon.png", nil, nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("favicon status = %d, body = %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("favicon Content-Type = %q, want image/png", contentType)
	}
	if body := response.Body.Bytes(); len(body) < 8 || !bytes.Equal(body[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		t.Fatal("favicon body does not start with a PNG signature")
	}
}

func TestStaticAssetsSkipGzipWhenNotAccepted(t *testing.T) {
	app := newTestApp(t)
	for _, acceptEncoding := range []string{"", "br, gzip;q=0"} {
		request := httptest.NewRequest(http.MethodGet, "/styles.css", nil)
		request.Header.Set("Accept-Encoding", acceptEncoding)
		response := httptest.NewRecorder()
		app.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("styles status = %d", response.Code)
		}
		if encoding := response.Header().Get("Content-Encoding"); encoding != "" {
			t.Fatalf("Content-Encoding = %q, want empty", encoding)
		}
		if !strings.Contains(response.Body.String(), ":root") {
			t.Fatalf("styles body does not look like CSS")
		}
	}
}

func TestFileUploadDownloadAndEvent(t *testing.T) {
	app := newTestApp(t)
	cookie, csrf := app.login(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("hello across devices")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/files", &body)
	request.AddCookie(cookie)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", response.Code, response.Body.String())
	}

	ticketResponse := performRequest(app.handler, http.MethodPost, "/api/files/1/download-ticket", nil, cookie, csrf)
	if ticketResponse.Code != http.StatusOK {
		t.Fatalf("ticket status = %d, body = %s", ticketResponse.Code, ticketResponse.Body.String())
	}
	var ticket struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(ticketResponse.Body.Bytes(), &ticket); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ticket.URL, "ticket") {
		t.Fatalf("download URL exposes ticket: %s", ticket.URL)
	}
	ticketCookies := ticketResponse.Result().Cookies()
	if len(ticketCookies) != 1 || !ticketCookies[0].HttpOnly {
		t.Fatalf("download ticket cookies = %#v, want one HttpOnly cookie", ticketCookies)
	}

	downloadRequest := httptest.NewRequest(http.MethodGet, ticket.URL, nil)
	downloadRequest.AddCookie(cookie)
	downloadRequest.AddCookie(ticketCookies[0])
	download := httptest.NewRecorder()
	app.handler.ServeHTTP(download, downloadRequest)
	if download.Code != http.StatusOK || download.Body.String() != "hello across devices" {
		t.Fatalf("download status = %d, body = %q", download.Code, download.Body.String())
	}
	if disposition := download.Header().Get("Content-Disposition"); !strings.Contains(disposition, "filename=hello.txt") {
		t.Fatalf("Content-Disposition = %q, want hello.txt filename", disposition)
	}

	items, err := app.db.listItems(context.Background(), app.userID, app.server.now())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Events) != 1 || items[0].Events[0].EventType != "download" {
		t.Fatalf("download events = %#v", items)
	}
}

func TestImagePreviewRequiresImageAndDoesNotRecordDownload(t *testing.T) {
	app := newTestApp(t)
	cookie, _ := app.login(t)
	if err := os.MkdirAll(app.server.cfg.uploadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := app.server.now()
	imageContent := []byte{0x89, 'P', 'N', 'G', '\r', '\n'}
	if err := os.WriteFile(filepath.Join(app.server.cfg.uploadDir, "stored-preview-image"), imageContent, 0o600); err != nil {
		t.Fatal(err)
	}
	imageID, err := app.db.createFile(context.Background(), fileRecord{
		UserID:     app.userID,
		StoredName: "stored-preview-image",
		FileName:   "photo.png",
		FileSize:   int64(len(imageContent)),
		MIMEType:   "image/png",
	}, "test device", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.server.cfg.uploadDir, "stored-preview-text"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	textID, err := app.db.createFile(context.Background(), fileRecord{
		UserID:     app.userID,
		StoredName: "stored-preview-text",
		FileName:   "note.txt",
		FileSize:   5,
		MIMEType:   "text/plain",
	}, "test device", now)
	if err != nil {
		t.Fatal(err)
	}

	preview := performRequest(app.handler, http.MethodGet, "/api/files/"+strconv.FormatInt(imageID, 10)+"/preview", nil, cookie, "")
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", preview.Code, preview.Body.String())
	}
	if preview.Body.String() != string(imageContent) {
		t.Fatalf("preview body = %q", preview.Body.String())
	}
	if contentType := preview.Header().Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("preview content type = %q", contentType)
	}
	if disposition := preview.Header().Get("Content-Disposition"); strings.Contains(disposition, "attachment") {
		t.Fatalf("preview disposition = %q", disposition)
	}
	if cacheControl := preview.Header().Get("Cache-Control"); cacheControl != "private, max-age=300" {
		t.Fatalf("preview cache control = %q", cacheControl)
	}
	if vary := preview.Header().Get("Vary"); vary != "Cookie" {
		t.Fatalf("preview vary = %q", vary)
	}
	blocked := performRequest(app.handler, http.MethodGet, "/api/files/"+strconv.FormatInt(textID, 10)+"/preview", nil, cookie, "")
	if blocked.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-image preview status = %d, body = %s", blocked.Code, blocked.Body.String())
	}
	items, err := app.db.listItems(context.Background(), app.userID, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == imageID && len(item.Events) != 0 {
			t.Fatalf("preview events = %#v, want no download event", item.Events)
		}
	}
}

func TestFileUploadRejectsUserQuotaExceeded(t *testing.T) {
	app := newTestApp(t)
	cookie, csrf := app.login(t)
	if err := app.db.setUserStorageQuota(context.Background(), app.userID, 3, app.server.now()); err != nil {
		t.Fatal(err)
	}

	response := uploadTestFile(t, app, cookie, csrf, "tiny.txt", []byte("tiny"))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload over quota status = %d, want %d; body = %s", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
	entries, err := os.ReadDir(app.server.cfg.uploadDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("upload directory entries after rejected upload = %d, want 0", len(entries))
	}
}

func TestFileDownloadRangeSkipsDownloadEvent(t *testing.T) {
	app := newTestApp(t)
	cookie, csrf := app.login(t)
	content := []byte("hello across devices")
	if err := os.MkdirAll(app.server.cfg.uploadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	storedName := "range-file"
	if err := os.WriteFile(filepath.Join(app.server.cfg.uploadDir, storedName), content, 0o600); err != nil {
		t.Fatal(err)
	}
	fileID, err := app.db.createFile(context.Background(), fileRecord{
		UserID:     app.userID,
		StoredName: storedName,
		FileName:   "range.txt",
		FileSize:   int64(len(content)),
		MIMEType:   "text/plain",
	}, "test device", app.server.now())
	if err != nil {
		t.Fatal(err)
	}
	downloadURL, ticketCookie := requestDownloadTicket(t, app, fileID, cookie, csrf)

	request := httptest.NewRequest(http.MethodGet, downloadURL, nil)
	request.AddCookie(cookie)
	request.AddCookie(ticketCookie)
	request.Header.Set("Range", "bytes=0-3")
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)

	if response.Code != http.StatusPartialContent {
		t.Fatalf("range download status = %d, body = %q", response.Code, response.Body.String())
	}
	if response.Body.String() != "hell" {
		t.Fatalf("range download body = %q, want %q", response.Body.String(), "hell")
	}
	if got := response.Header().Get("Content-Range"); got != "bytes 0-3/20" {
		t.Fatalf("Content-Range = %q, want bytes 0-3/20", got)
	}
	if got := response.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", got)
	}

	items, err := app.db.listItems(context.Background(), app.userID, app.server.now())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Events) != 0 {
		t.Fatalf("range download events = %#v, want no download event", items)
	}
}

func TestFileDownloadContentDispositionSupportsChineseFilename(t *testing.T) {
	app := newTestApp(t)
	cookie, csrf := app.login(t)
	content := []byte("report")
	if err := os.MkdirAll(app.server.cfg.uploadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	storedName := "chinese-file"
	if err := os.WriteFile(filepath.Join(app.server.cfg.uploadDir, storedName), content, 0o600); err != nil {
		t.Fatal(err)
	}
	fileName := "轻递报告.txt"
	fileID, err := app.db.createFile(context.Background(), fileRecord{
		UserID:     app.userID,
		StoredName: storedName,
		FileName:   fileName,
		FileSize:   int64(len(content)),
		MIMEType:   "text/plain",
	}, "test device", app.server.now())
	if err != nil {
		t.Fatal(err)
	}
	downloadURL, ticketCookie := requestDownloadTicket(t, app, fileID, cookie, csrf)

	request := httptest.NewRequest(http.MethodGet, downloadURL, nil)
	request.AddCookie(cookie)
	request.AddCookie(ticketCookie)
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("download status = %d, body = %q", response.Code, response.Body.String())
	}
	disposition := response.Header().Get("Content-Disposition")
	escapedName := url.PathEscape(fileName)
	if !strings.Contains(disposition, "filename*=") || !strings.Contains(disposition, escapedName) {
		t.Fatalf("Content-Disposition = %q, want encoded filename %q", disposition, escapedName)
	}
}

func TestCleanupExpiredRemovesUnusedDownloadTickets(t *testing.T) {
	app := newTestApp(t)
	current := app.server.now()
	app.server.now = func() time.Time { return current }
	cookie, csrf := app.login(t)

	fileID, err := app.db.createFile(context.Background(), fileRecord{
		UserID:     app.userID,
		StoredName: "stored-file",
		FileName:   "hello.txt",
		FileSize:   5,
		MIMEType:   "text/plain",
	}, "test device", current)
	if err != nil {
		t.Fatal(err)
	}

	response := performRequest(app.handler, http.MethodPost, "/api/files/"+strconv.FormatInt(fileID, 10)+"/download-ticket", nil, cookie, csrf)
	if response.Code != http.StatusOK {
		t.Fatalf("ticket status = %d, body = %s", response.Code, response.Body.String())
	}

	app.server.ticketMu.Lock()
	ticketCount := len(app.server.tickets)
	app.server.ticketMu.Unlock()
	if ticketCount != 1 {
		t.Fatalf("ticket count = %d, want 1", ticketCount)
	}

	current = current.Add(2 * time.Minute)
	if err := app.server.cleanupExpired(context.Background()); err != nil {
		t.Fatal(err)
	}

	app.server.ticketMu.Lock()
	ticketCount = len(app.server.tickets)
	app.server.ticketMu.Unlock()
	if ticketCount != 0 {
		t.Fatalf("expired ticket count = %d, want 0", ticketCount)
	}
}

func TestDownloadTicketRejectsWhenLiveTicketCapReached(t *testing.T) {
	app := newTestApp(t)
	current := app.server.now()
	app.server.now = func() time.Time { return current }
	cookie, csrf := app.login(t)

	fileID, err := app.db.createFile(context.Background(), fileRecord{
		UserID:     app.userID,
		StoredName: "stored-file",
		FileName:   "hello.txt",
		FileSize:   5,
		MIMEType:   "text/plain",
	}, "test device", current)
	if err != nil {
		t.Fatal(err)
	}

	app.server.ticketMu.Lock()
	for index := 0; index < maxDownloadTickets; index++ {
		app.server.tickets["seed-"+strconv.Itoa(index)] = downloadTicket{fileID: fileID, sessionHash: "other-session", expiresAt: current.Add(time.Minute)}
	}
	app.server.ticketMu.Unlock()

	response := performRequest(app.handler, http.MethodPost, "/api/files/"+strconv.FormatInt(fileID, 10)+"/download-ticket", nil, cookie, csrf)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("ticket status at cap = %d, want %d; body = %s", response.Code, http.StatusTooManyRequests, response.Body.String())
	}
	app.server.ticketMu.Lock()
	ticketCount := len(app.server.tickets)
	app.server.ticketMu.Unlock()
	if ticketCount > maxDownloadTickets {
		t.Fatalf("ticket count = %d, want <= %d", ticketCount, maxDownloadTickets)
	}
}

func TestCleanupExpiredContent(t *testing.T) {
	app := newTestApp(t)
	if err := os.MkdirAll(app.server.cfg.uploadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	storedName := "expired-file"
	path := filepath.Join(app.server.cfg.uploadDir, storedName)
	if err := os.WriteFile(path, []byte("expired"), 0o600); err != nil {
		t.Fatal(err)
	}
	expired := app.server.now().Add(-time.Hour).Unix()
	_, err := app.db.db.Exec(`
		INSERT INTO files(user_id, original_name, stored_name, size_bytes, mime_type, uploader_device, created_at, expires_at)
		VALUES(?, 'old.txt', ?, 7, 'text/plain', 'test', ?, ?)
	`, app.userID, storedName, expired-3600, expired)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.server.cleanupExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired file still exists, stat error = %v", err)
	}
	items, err := app.db.listItems(context.Background(), app.userID, app.server.now())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expired items remain: %#v", items)
	}
}
