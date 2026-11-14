package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata"

	"rhythm/frontend"
	"rhythm/internal/accounts"
	"rhythm/internal/filestore"
	"rhythm/internal/httpapi"
	"rhythm/internal/postgresstore"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	location, err := time.LoadLocation(env("TIME_ZONE", "Europe/Moscow"))
	if err != nil {
		return fmt.Errorf("load time zone: %w", err)
	}
	var repository httpapi.Repository
	var credentials accounts.Repository
	var secret []byte
	storage := "file"
	if url := os.Getenv("DATABASE_URL"); url != "" {
		store, err := postgresstore.Open(ctx, url)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.Check(ctx); err != nil {
			return err
		}
		secret, err = store.SessionSecret(ctx, os.Getenv("SESSION_SECRET"))
		if err != nil {
			return err
		}
		repository, credentials, storage = store, store, "postgresql"
	} else {
		store, err := filestore.New(env("DATA_DIR", ".data"))
		if err != nil {
			return err
		}
		secret, err = store.SessionSecret(ctx, os.Getenv("SESSION_SECRET"))
		if err != nil {
			return err
		}
		repository, credentials = store, store
	}
	accountService, err := accounts.New(credentials)
	if err != nil {
		return err
	}
	static, err := fs.Sub(frontend.Assets, "static")
	if err != nil {
		return fmt.Errorf("load website: %w", err)
	}
	ai, err := configureAI(os.Getenv)
	if err != nil {
		return err
	}
	aiHourlyLimit, err := strconv.Atoi(env("AI_HOURLY_LIMIT", "60"))
	if err != nil || aiHourlyLimit < 1 || aiHourlyLimit > 10000 {
		return errors.New("AI_HOURLY_LIMIT must be an integer between 1 and 10000")
	}
	handler, err := httpapi.New(httpapi.Config{Repository: repository, Storage: storage, Secret: secret, BotToken: os.Getenv("TELEGRAM_BOT_TOKEN"), PublicURL: os.Getenv("PUBLIC_URL"), Location: location, Static: static, Logger: logger, AI: ai.client, AIProvider: ai.provider, AIModel: ai.model, AITimeout: ai.timeout, AIHourlyLimit: aiHourlyLimit, Accounts: accountService, TrustedProxyCIDRs: os.Getenv("TRUSTED_PROXY_CIDRS")})
	if err != nil {
		return err
	}
	address := env("ADDR", "127.0.0.1:8080")
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: max(15*time.Second, ai.timeout+10*time.Second), IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	logger.Info("rhythm listening", "address", listener.Addr().String(), "timezone", location.String(), "storage", storage, "telegram_configured", os.Getenv("TELEGRAM_BOT_TOKEN") != "", "ai_configured", ai.client != nil, "ai_provider", ai.provider, "ai_model", ai.model)
	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		closeErr := server.Close()
		<-served
		return errors.Join(fmt.Errorf("shutdown: %w", err), closeErr)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
