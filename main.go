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
		return fmt.Errorf("未知命令 %q；可用命令：serve、user set-password <username>", os.Args[1])
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	app := newServer(cfg, store, logger)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go app.cleanupLoop(ctx)

	httpServer := &http.Server{
		Addr:              cfg.addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Hour,
		WriteTimeout:      2 * time.Hour,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

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

func runUserCommand(store *database, args []string) error {
	if len(args) != 2 || args[0] != "set-password" {
		return errors.New("用法：APP_ADMIN_PASSWORD='...' go run . user set-password <username>")
	}
	username := strings.TrimSpace(args[1])
	if username == "" || len(username) > 64 {
		return errors.New("用户名不能为空且不能超过 64 个字符")
	}
	password := os.Getenv("APP_ADMIN_PASSWORD")
	if password == "" {
		return errors.New("请通过 APP_ADMIN_PASSWORD 环境变量提供密码")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	if err := store.setUserPassword(context.Background(), username, hash, time.Now().UTC()); err != nil {
		return fmt.Errorf("保存账号: %w", err)
	}
	fmt.Printf("账号 %q 已创建或更新。\n", username)
	return nil
}
