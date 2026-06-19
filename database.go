package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

type database struct {
	db *sql.DB
}

var errUserFileQuotaExceeded = errors.New("user file quota exceeded")
var errUserTextQuotaExceeded = errors.New("user text quota exceeded")

type user struct {
	ID           int64
	Username     string
	Role         string
	PasswordHash string
}

type session struct {
	TokenHash string
	UserID    int64
	Username  string
	Role      string
	CSRFToken string
	ExpiresAt time.Time
}

type eventRecord struct {
	ID          int64     `json:"id"`
	EventType   string    `json:"eventType"`
	DeviceLabel string    `json:"deviceLabel"`
	CreatedAt   time.Time `json:"createdAt"`
}

type itemRecord struct {
	ID             int64         `json:"id"`
	Kind           string        `json:"kind"`
	Text           string        `json:"text,omitempty"`
	FileName       string        `json:"fileName,omitempty"`
	FileSize       int64         `json:"fileSize,omitempty"`
	MIMEType       string        `json:"mimeType,omitempty"`
	CreatedAt      time.Time     `json:"createdAt"`
	ExpiresAt      time.Time     `json:"expiresAt"`
	UploaderDevice string        `json:"uploaderDevice"`
	Events         []eventRecord `json:"events"`
}

type fileRecord struct {
	ID         int64
	UserID     int64
	StoredName string
	FileName   string
	FileSize   int64
	MIMEType   string
}

type adminUserRecord struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	Role         string     `json:"role"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt"`
	LastUploadAt *time.Time `json:"lastUploadAt"`
	TextCount    int64      `json:"textCount"`
	FileCount    int64      `json:"fileCount"`
	LoginCount   int64      `json:"loginCount"`
}

func openDatabase(path string) (*database, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &database{db: db}
	if err := store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (d *database) close() error {
	return d.db.Close()
}

func (d *database) ping(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

func (d *database) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL DEFAULT 'user',
			password_hash TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			csrf_token TEXT NOT NULL,
			device_label TEXT NOT NULL,
			ip_address TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS sessions_expires_idx ON sessions(expires_at)`,
		`CREATE TABLE IF NOT EXISTS login_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			device_label TEXT NOT NULL,
			ip_address TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS login_events_user_created_idx ON login_events(user_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS texts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			body TEXT NOT NULL,
			uploader_device TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS texts_user_created_idx ON texts(user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS texts_expires_idx ON texts(expires_at)`,
		`CREATE TABLE IF NOT EXISTS files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			original_name TEXT NOT NULL,
			stored_name TEXT NOT NULL UNIQUE,
			size_bytes INTEGER NOT NULL,
			mime_type TEXT NOT NULL,
			uploader_device TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS files_user_created_idx ON files(user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS files_expires_idx ON files(expires_at)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			item_kind TEXT NOT NULL CHECK(item_kind IN ('text', 'file')),
			item_id INTEGER NOT NULL,
			event_type TEXT NOT NULL CHECK(event_type IN ('copy', 'download')),
			device_label TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS events_item_idx ON events(user_id, item_kind, item_id, created_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := d.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("run database migration: %w", err)
		}
	}
	if err := d.ensureColumn(ctx, "users", "role", "TEXT NOT NULL DEFAULT 'user'"); err != nil {
		return err
	}
	if _, err := d.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE lower(username) = ?`, roleAdmin, adminUsername); err != nil {
		return fmt.Errorf("reserve admin role: %w", err)
	}
	return nil
}

func (d *database) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := d.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan %s columns: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan %s columns: %w", table, err)
	}
	if _, err := d.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition)); err != nil {
		return fmt.Errorf("add %s.%s column: %w", table, column, err)
	}
	return nil
}

func (d *database) setUserPassword(ctx context.Context, username, passwordHash string, now time.Time) error {
	if isReservedAdminUsername(username) {
		return errReservedAdminUsername
	}
	return d.setUserPasswordWithRole(ctx, username, passwordHash, roleUser, now)
}

func (d *database) setAdminPassword(ctx context.Context, passwordHash string, now time.Time) error {
	return d.setUserPasswordWithRole(ctx, adminUsername, passwordHash, roleAdmin, now)
}

func (d *database) setUserPasswordWithRole(ctx context.Context, username, passwordHash, role string, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users(username, role, password_hash, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET role = excluded.role, password_hash = excluded.password_hash, updated_at = excluded.updated_at
	`, username, role, passwordHash, now.Unix(), now.Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM sessions
		WHERE user_id = (SELECT id FROM users WHERE username = ?)
	`, username); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *database) findUser(ctx context.Context, username string) (user, error) {
	var result user
	err := d.db.QueryRowContext(ctx, `SELECT id, username, role, password_hash FROM users WHERE username = ?`, username).
		Scan(&result.ID, &result.Username, &result.Role, &result.PasswordHash)
	return result, err
}

func (d *database) createSession(ctx context.Context, tokenHash string, userID int64, csrfToken, device, ip string, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions(token_hash, user_id, csrf_token, device_label, ip_address, created_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, tokenHash, userID, csrfToken, device, ip, now.Unix(), now.Add(sessionTTL).Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO login_events(user_id, device_label, ip_address, created_at)
		VALUES(?, ?, ?, ?)
	`, userID, device, ip, now.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *database) getSession(ctx context.Context, hash string, now time.Time) (session, error) {
	var result session
	var expires int64
	err := d.db.QueryRowContext(ctx, `
		SELECT s.token_hash, s.user_id, u.username, u.role, s.csrf_token, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?
	`, hash, now.Unix()).Scan(&result.TokenHash, &result.UserID, &result.Username, &result.Role, &result.CSRFToken, &expires)
	result.ExpiresAt = time.Unix(expires, 0).UTC()
	return result, err
}

func (d *database) deleteSession(ctx context.Context, hash string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash)
	return err
}

func (d *database) createText(ctx context.Context, userID int64, body, device string, now time.Time) (int64, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	bodyRunes := utf8.RuneCountInString(body)
	var usedRunes int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(length(body)), 0) FROM texts WHERE user_id = ? AND expires_at > ?
	`, userID, now.Unix()).Scan(&usedRunes); err != nil {
		return 0, err
	}
	if bodyRunes > maxUserTextRunes || usedRunes > maxUserTextRunes-bodyRunes {
		return 0, errUserTextQuotaExceeded
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO texts(user_id, body, uploader_device, created_at, expires_at) VALUES(?, ?, ?, ?, ?)
	`, userID, body, device, now.Unix(), now.Add(textTTL).Unix())
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (d *database) createFile(ctx context.Context, record fileRecord, device string, now time.Time) (int64, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var usedBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(size_bytes), 0) FROM files WHERE user_id = ? AND expires_at > ?
	`, record.UserID, now.Unix()).Scan(&usedBytes); err != nil {
		return 0, err
	}
	if record.FileSize < 0 || record.FileSize > maxUserFileBytes || usedBytes > maxUserFileBytes-record.FileSize {
		return 0, errUserFileQuotaExceeded
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO files(user_id, original_name, stored_name, size_bytes, mime_type, uploader_device, created_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, record.UserID, record.FileName, record.StoredName, record.FileSize, record.MIMEType, device, now.Unix(), now.Add(fileTTL).Unix())
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (d *database) getFile(ctx context.Context, userID, fileID int64, now time.Time) (fileRecord, error) {
	var result fileRecord
	err := d.db.QueryRowContext(ctx, `
		SELECT id, user_id, stored_name, original_name, size_bytes, mime_type
		FROM files WHERE id = ? AND user_id = ? AND expires_at > ?
	`, fileID, userID, now.Unix()).Scan(&result.ID, &result.UserID, &result.StoredName, &result.FileName, &result.FileSize, &result.MIMEType)
	return result, err
}

func (d *database) addEvent(ctx context.Context, userID int64, kind string, itemID int64, eventType, device string, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	table := "texts"
	if kind == "file" {
		table = "files"
	}
	var exists int
	query := fmt.Sprintf(`SELECT 1 FROM %s WHERE id = ? AND user_id = ? AND expires_at > ?`, table)
	if err := tx.QueryRowContext(ctx, query, itemID, userID, now.Unix()).Scan(&exists); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO events(user_id, item_kind, item_id, event_type, device_label, created_at)
		VALUES(?, ?, ?, ?, ?, ?)
	`, userID, kind, itemID, eventType, device, now.Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM events
		WHERE user_id = ? AND item_kind = ? AND item_id = ?
		  AND id NOT IN (
			SELECT id FROM events
			WHERE user_id = ? AND item_kind = ? AND item_id = ?
			ORDER BY created_at DESC, id DESC
			LIMIT ?
		  )
	`, userID, kind, itemID, userID, kind, itemID, maxItemEvents); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *database) deleteItem(ctx context.Context, userID int64, kind string, itemID int64) (string, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	storedName := ""
	if kind == "file" {
		if err := tx.QueryRowContext(ctx, `SELECT stored_name FROM files WHERE id = ? AND user_id = ?`, itemID, userID).Scan(&storedName); err != nil {
			return "", err
		}
	}
	table := "texts"
	if kind == "file" {
		table = "files"
	}
	result, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = ? AND user_id = ?`, table), itemID, userID)
	if err != nil {
		return "", err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return "", sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE user_id = ? AND item_kind = ? AND item_id = ?`, userID, kind, itemID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return storedName, nil
}

func (d *database) listAdminUsers(ctx context.Context, now time.Time) ([]adminUserRecord, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT
			u.id,
			u.username,
			u.role,
			u.created_at,
			u.updated_at,
			(SELECT MAX(created_at) FROM login_events WHERE user_id = u.id) AS last_login_at,
			(
				SELECT MAX(created_at) FROM (
					SELECT created_at FROM texts WHERE user_id = u.id
					UNION ALL
					SELECT created_at FROM files WHERE user_id = u.id
				)
			) AS last_upload_at,
			(SELECT COUNT(*) FROM texts WHERE user_id = u.id AND expires_at > ?) AS text_count,
			(SELECT COUNT(*) FROM files WHERE user_id = u.id AND expires_at > ?) AS file_count,
			(SELECT COUNT(*) FROM login_events WHERE user_id = u.id) AS login_count
		FROM users u
		ORDER BY CASE u.role WHEN 'admin' THEN 0 ELSE 1 END, u.username COLLATE NOCASE
	`, now.Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]adminUserRecord, 0)
	for rows.Next() {
		var record adminUserRecord
		var created, updated int64
		var lastLogin, lastUpload sql.NullInt64
		if err := rows.Scan(
			&record.ID,
			&record.Username,
			&record.Role,
			&created,
			&updated,
			&lastLogin,
			&lastUpload,
			&record.TextCount,
			&record.FileCount,
			&record.LoginCount,
		); err != nil {
			return nil, err
		}
		record.CreatedAt = time.Unix(created, 0).UTC()
		record.UpdatedAt = time.Unix(updated, 0).UTC()
		if lastLogin.Valid {
			value := time.Unix(lastLogin.Int64, 0).UTC()
			record.LastLoginAt = &value
		}
		if lastUpload.Valid {
			value := time.Unix(lastUpload.Int64, 0).UTC()
			record.LastUploadAt = &value
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (d *database) findUserByID(ctx context.Context, userID int64) (user, error) {
	var result user
	err := d.db.QueryRowContext(ctx, `SELECT id, username, role, password_hash FROM users WHERE id = ?`, userID).
		Scan(&result.ID, &result.Username, &result.Role, &result.PasswordHash)
	return result, err
}

func (d *database) setUserPasswordByID(ctx context.Context, userID int64, passwordHash string, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?
	`, passwordHash, now.Unix(), userID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *database) deleteUser(ctx context.Context, userID int64) ([]string, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, userID).Scan(&role); err != nil {
		return nil, err
	}
	if role == roleAdmin {
		return nil, errReservedAdminUsername
	}

	rows, err := tx.QueryContext(ctx, `SELECT stored_name FROM files WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	var storedNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		storedNames = append(storedNames, name)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return storedNames, nil
}

func (d *database) listItems(ctx context.Context, userID int64, now time.Time) ([]itemRecord, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, 'text', body, '', 0, '', uploader_device, created_at, expires_at FROM texts
		WHERE user_id = ? AND expires_at > ?
		UNION ALL
		SELECT id, 'file', '', original_name, size_bytes, mime_type, uploader_device, created_at, expires_at FROM files
		WHERE user_id = ? AND expires_at > ?
		ORDER BY created_at DESC
	`, userID, now.Unix(), userID, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]itemRecord, 0)
	for rows.Next() {
		var item itemRecord
		var created, expires int64
		if err := rows.Scan(&item.ID, &item.Kind, &item.Text, &item.FileName, &item.FileSize, &item.MIMEType, &item.UploaderDevice, &created, &expires); err != nil {
			return nil, err
		}
		item.CreatedAt = time.Unix(created, 0).UTC()
		item.ExpiresAt = time.Unix(expires, 0).UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range items {
		items[index].Events, err = d.listEvents(ctx, userID, items[index].Kind, items[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (d *database) listEvents(ctx context.Context, userID int64, kind string, itemID int64) ([]eventRecord, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, event_type, device_label, created_at FROM events
		WHERE user_id = ? AND item_kind = ? AND item_id = ? ORDER BY created_at DESC
	`, userID, kind, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]eventRecord, 0)
	for rows.Next() {
		var event eventRecord
		var created int64
		if err := rows.Scan(&event.ID, &event.EventType, &event.DeviceLabel, &created); err != nil {
			return nil, err
		}
		event.CreatedAt = time.Unix(created, 0).UTC()
		events = append(events, event)
	}
	return events, rows.Err()
}

func (d *database) expiredFileNames(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT stored_name FROM files WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (d *database) deleteExpired(ctx context.Context, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`DELETE FROM events WHERE item_kind = 'text' AND item_id IN (SELECT id FROM texts WHERE expires_at <= ?)`,
		`DELETE FROM events WHERE item_kind = 'file' AND item_id IN (SELECT id FROM files WHERE expires_at <= ?)`,
		`DELETE FROM texts WHERE expires_at <= ?`,
		`DELETE FROM files WHERE expires_at <= ?`,
		`DELETE FROM sessions WHERE expires_at <= ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, now.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func isNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
