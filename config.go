package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	maxFileBytes       = int64(1 << 30)
	maxUserFileBytes   = 2 * maxFileBytes
	maxTextRunes       = 100_000
	maxUserTextRunes   = 2 * maxTextRunes
	maxItemEvents      = 20
	maxDownloadTickets = 256
	textTTL            = 30 * 24 * time.Hour
	fileTTL            = 7 * 24 * time.Hour
	sessionTTL         = 7 * 24 * time.Hour
)

type config struct {
	addr              string
	databasePath      string
	uploadDir         string
	cookieSecure      bool
	trustProxyHeaders bool
	cleanupPeriod     time.Duration
}

func loadConfig() (config, error) {
	dataDir, err := defaultDataDir()
	if err != nil {
		return config{}, err
	}
	cfg := config{
		addr:          envOr("APP_ADDR", "127.0.0.1:8080"),
		databasePath:  envOr("APP_DB_PATH", filepath.Join(dataDir, "share.db")),
		uploadDir:     envOr("APP_UPLOAD_DIR", filepath.Join(dataDir, "uploads")),
		cookieSecure:  true,
		cleanupPeriod: time.Hour,
	}

	if raw := os.Getenv("APP_COOKIE_SECURE"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse APP_COOKIE_SECURE: %w", err)
		}
		cfg.cookieSecure = value
	}
	if raw := os.Getenv("APP_TRUST_PROXY_HEADERS"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse APP_TRUST_PROXY_HEADERS: %w", err)
		}
		cfg.trustProxyHeaders = value
	}

	return cfg, nil
}

func defaultDataDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	return filepath.Join(base, "file-text-share"), nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
