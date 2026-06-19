package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := openDatabase(cfg.databasePath)
	if err != nil {
		return err
	}
	defer store.close()

	if len(os.Args) > 1 && os.Args[1] == "user" {
		return runUserCommand(store, os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		return fmt.Errorf("未知命令 %q；可用命令：serve、user set-password <username>、user set-admin-password", os.Args[1])
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	app := newServer(cfg, store, logger)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go app.cleanupLoop(ctx)

	httpServer := newHTTPServer(cfg, app.routes())

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server started", "address", cfg.addr)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func newHTTPServer(cfg config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Minute,
		WriteTimeout:      15 * time.Minute,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func runUserCommand(store *database, args []string) error {
	if len(args) == 1 && args[0] == "set-admin-password" {
		return setAccountPassword(store, adminUsername, roleAdmin)
	}
	if len(args) == 2 && args[0] == "set-password" {
		return setAccountPassword(store, args[1], roleUser)
	}
	return errors.New("用法：APP_ADMIN_PASSWORD='...' go run . user set-password <username>；或 APP_ADMIN_PASSWORD='...' go run . user set-admin-password")
}

func setAccountPassword(store *database, username, role string) error {
	username = strings.TrimSpace(username)
	if username == "" || len(username) > 64 {
		return errors.New("用户名不能为空且不能超过 64 个字符")
	}
	if role == roleUser && isReservedAdminUsername(username) {
		return errReservedAdminUsername
	}
	password := os.Getenv("APP_ADMIN_PASSWORD")
	if password == "" {
		return errors.New("请通过 APP_ADMIN_PASSWORD 环境变量提供密码")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	var saveErr error
	if role == roleAdmin {
		saveErr = store.setAdminPassword(context.Background(), hash, time.Now().UTC())
	} else {
		saveErr = store.setUserPassword(context.Background(), username, hash, time.Now().UTC())
	}
	if saveErr != nil {
		return fmt.Errorf("保存账号: %w", saveErr)
	}
	fmt.Printf("账号 %q 已创建或更新。\n", username)
	return nil
}
