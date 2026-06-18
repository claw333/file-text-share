package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		Items []itemRecord `json:"items"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listResult); err != nil {
		t.Fatal(err)
	}
	if len(listResult.Items) != 1 || len(listResult.Items[0].Events) != 1 {
		t.Fatalf("listed items = %#v", listResult.Items)
	}

	deleted := performRequest(app.handler, http.MethodDelete, "/api/texts/1", nil, cookie, csrf)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", deleted.Code)
	}
	if createResult.ID != 1 {
		t.Fatalf("created ID = %d, want 1", createResult.ID)
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

	items, err := app.db.listItems(context.Background(), app.userID, app.server.now())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Events) != 1 || items[0].Events[0].EventType != "download" {
		t.Fatalf("download events = %#v", items)
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
