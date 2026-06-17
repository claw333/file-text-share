package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	body := strings.NewReader(`{"username":"demo","password":"Prototype#2026"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/login", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body.String())
	}
	result := struct {
		CSRFToken string `json:"csrfToken"`
	}{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %d, want 1", len(cookies))
	}
	return cookies[0], result.CSRFToken
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
