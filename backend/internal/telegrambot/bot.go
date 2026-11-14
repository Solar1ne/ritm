// Package telegrambot provides the opt-in private command entry point to Rhythm.
// Run has one owner and must not be called concurrently for the same bot token.
package telegrambot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	token string
	http  HTTPClient
	base  string
}

func NewClient(token string, client HTTPClient) (*Client, error) {
	if token == "" || strings.ContainsAny(token, "/?#\r\n ") {
		return nil, errors.New("invalid TELEGRAM_BOT_TOKEN")
	}
	if client == nil {
		return nil, errors.New("HTTP client is required")
	}
	return &Client{token: token, http: client, base: "https://api.telegram.org"}, nil
}

type APIError struct{ Code int }

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram API rejected request (code %d)", e.Code)
}

func (c *Client) call(ctx context.Context, method string, payload any, result any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("encode Telegram request")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return errors.New("create Telegram request")
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("telegram transport failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return errors.New("read Telegram response")
	}
	if len(raw) > 1<<20 {
		return errors.New("telegram response exceeds limit")
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Code   int             `json:"error_code"`
		Result json.RawMessage `json:"result"`
	}
	if response.StatusCode != http.StatusOK {
		return &APIError{Code: response.StatusCode}
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return errors.New("invalid Telegram response")
	}
	if !envelope.OK {
		return &APIError{Code: envelope.Code}
	}
	if result != nil && json.Unmarshal(envelope.Result, result) != nil {
		return errors.New("invalid Telegram result")
	}
	return nil
}

func MiniURL(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("PUBLIC_URL must be an HTTPS origin")
	}
	return strings.TrimSuffix(u.String(), "/") + "/mini", nil
}

type update struct {
	ID      int64    `json:"update_id"`
	Message *message `json:"message"`
}
type message struct {
	Text string `json:"text"`
	From struct {
		Bot bool `json:"is_bot"`
	} `json:"from"`
	Chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
}

func reply(m *message, username string) string {
	if m == nil || m.Chat.Type != "private" || m.Chat.ID <= 0 || m.From.Bot {
		return ""
	}
	fields := strings.Fields(m.Text)
	if len(fields) == 0 {
		return ""
	}
	command, target, hasTarget := strings.Cut(fields[0], "@")
	if hasTarget && !strings.EqualFold(target, username) {
		return ""
	}
	switch command {
	case "/start":
		return "Ритм — ваш план тренировок и дневник самочувствия. Откройте приложение, чтобы начать."
	case "/help":
		return "Откройте «Мой ритм»: настройте план, отмечайте самочувствие и сохраняйте тренировки. /privacy — о данных."
	case "/privacy":
		return "Вход на сайте и через Telegram — отдельные аккаунты. Дневник доступен в вашем аккаунте; экспорт и очистка дневника находятся в настройках. AI включается только с вашим согласием. Бот отвечает только на личные команды и не рассылает напоминания."
	default:
		return ""
	}
}

// Run polls until cancellation. Only one process may consume this token's updates.
// Offsets advance after each attempted reply, avoiding retries within this Run.
// A crash before the next poll acknowledges an offset can replay that batch on
// restart. An isolated failed reply can be requested again by the user.
func Run(ctx context.Context, c *Client, origin string) error {
	mini, err := MiniURL(origin)
	if err != nil {
		return err
	}
	var me struct {
		Username string `json:"username"`
	}
	if err = c.call(ctx, "getMe", struct{}{}, &me); err != nil {
		return err
	}
	if me.Username == "" {
		return errors.New("telegram bot username is missing")
	}
	var hook struct {
		URL string `json:"url"`
	}
	if err = c.call(ctx, "getWebhookInfo", struct{}{}, &hook); err != nil {
		return err
	}
	if hook.URL != "" {
		return errors.New("telegram webhook exists; choose its owner before starting polling")
	}
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		var updates []update
		err = c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 30, "limit": 100, "allowed_updates": []string{"message"}}, &updates)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var api *APIError
			if errors.As(err, &api) && (api.Code == 401 || api.Code == 403 || api.Code == 409) {
				return err
			}
			if err = wait(ctx, backoff); err != nil {
				return err
			}
			backoff = min(30*time.Second, backoff*2)
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			if u.ID < offset {
				continue
			}
			offset = u.ID + 1
			text := reply(u.Message, me.Username)
			if text == "" {
				continue
			}
			err = c.call(ctx, "sendMessage", map[string]any{"chat_id": u.Message.Chat.ID, "text": text, "reply_markup": map[string]any{"inline_keyboard": [][]any{{map[string]any{"text": "Мой ритм", "web_app": map[string]string{"url": mini}}}}}}, nil)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var api *APIError
			if errors.As(err, &api) && (api.Code == 401 || api.Code == 409) {
				return err
			}
		}
		// A faulty upstream returning empty batches immediately cannot create a busy loop.
		if len(updates) == 0 {
			if err = wait(ctx, 250*time.Millisecond); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
