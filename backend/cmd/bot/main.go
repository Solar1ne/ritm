package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rhythm/internal/telegrambot"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client, err := telegrambot.NewClient(os.Getenv("TELEGRAM_BOT_TOKEN"), &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
	if err != nil {
		return err
	}
	err = telegrambot.Run(ctx, client, os.Getenv("PUBLIC_URL"))
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
