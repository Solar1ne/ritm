// Package deepseek adapts the optional coach to DeepSeek Chat Completions.
// Credentials and provider response bodies never enter application logs.
package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"rhythm/internal/aicoach"
)

const (
	DefaultModel     = "deepseek-flash"
	defaultEndpoint  = "https://api.deepseek.com/chat/completions"
	maxResponseBytes = 1024 * 1024
)

var (
	ErrConfiguration = errors.New("неверная конфигурация DeepSeek")
	ErrUnavailable   = errors.New("DeepSeek сейчас недоступен")
	ErrRefused       = errors.New("DeepSeek не смог выполнить этот запрос")
	ErrIncomplete    = errors.New("DeepSeek не завершил ответ; данные не изменены")
)

// HTTPError exposes only a status code; provider error bodies stay unread.
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string {
	return fmt.Sprintf("DeepSeek недоступен (HTTP %d)", e.StatusCode)
}
func (e *HTTPError) Unwrap() error   { return ErrUnavailable }
func (e *HTTPError) HTTPStatus() int { return e.StatusCode }

type Config struct {
	APIKey string
	Model  string
	// Injection points for controlled tests, not application environment settings.
	HTTPClient *http.Client
	Endpoint   string
}

// Client is safe for concurrent use and owns an immutable HTTP client copy.
// Callers retain ownership of inputs and must not mutate them during Generate.
type Client struct {
	apiKey     string
	model      string
	endpoint   string
	httpClient *http.Client
}

func New(cfg Config) (*Client, error) {
	if !token(cfg.APIKey, 512, false) {
		return nil, ErrConfiguration
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if !token(cfg.Model, 128, true) {
		return nil, ErrConfiguration
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultEndpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/chat/completions" {
		return nil, ErrConfiguration
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || cfg.HTTPClient == nil || ip == nil || !ip.IsLoopback() {
			return nil, ErrConfiguration
		}
	}
	client := http.Client{}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{apiKey: cfg.APIKey, model: cfg.Model, endpoint: cfg.Endpoint, httpClient: &client}, nil
}

// Generate makes one bounded request, with no retries, persistence or tools.
// Consent, concurrency quotas and deadlines belong to the calling use case.
func (c *Client) Generate(ctx context.Context, input aicoach.Request) (aicoach.Result, error) {
	if err := ctx.Err(); err != nil {
		return aicoach.Result{}, err
	}
	if err := aicoach.ValidateRequest(input); err != nil {
		return aicoach.Result{}, err
	}
	if c == nil || c.httpClient == nil {
		return aicoach.Result{}, ErrConfiguration
	}
	data, err := json.Marshal(input)
	if err != nil {
		return aicoach.Result{}, aicoach.ErrInvalidRequest
	}
	wire := completionRequest{Model: c.model, MaxTokens: 2200, Messages: []message{
		{Role: "system", Content: aicoach.Instructions + "\nReturn a JSON object matching this JSON Schema, without markdown: " + aicoach.ResultSchema},
		{Role: "user", Content: string(data)},
	}}
	wire.ResponseFormat.Type = "json_object"
	wire.Thinking.Type = "disabled"
	body, err := json.Marshal(wire)
	if err != nil {
		return aicoach.Result{}, ErrConfiguration
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return aicoach.Result{}, ErrConfiguration
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return aicoach.Result{}, transportError(ctx)
	}
	if resp.StatusCode != http.StatusOK {
		// Error bodies may echo credentials or diary text: never read or log them.
		if err := resp.Body.Close(); err != nil {
			return aicoach.Result{}, transportError(ctx)
		}
		if err := ctx.Err(); err != nil {
			return aicoach.Result{}, err
		}
		return aicoach.Result{}, &HTTPError{StatusCode: resp.StatusCode}
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil {
		return aicoach.Result{}, transportError(ctx)
	}
	if err := ctx.Err(); err != nil {
		return aicoach.Result{}, err
	}
	if len(data) > maxResponseBytes || !utf8.Valid(data) {
		return aicoach.Result{}, aicoach.ErrInvalidResult
	}
	result, err := parseResponse(data)
	if err != nil {
		return aicoach.Result{}, err
	}
	if err := aicoach.ValidateResult(input, result); err != nil {
		return aicoach.Result{}, err
	}
	return result, nil
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionRequest struct {
	Model          string    `json:"model"`
	Messages       []message `json:"messages"`
	MaxTokens      int       `json:"max_tokens"`
	Stream         bool      `json:"stream"`
	ResponseFormat struct {
		Type string `json:"type"`
	} `json:"response_format"`
	Thinking struct {
		Type string `json:"type"`
	} `json:"thinking"`
}

func parseResponse(data []byte) (aicoach.Result, error) {
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role      string          `json:"role"`
				Content   string          `json:"content"`
				Refusal   string          `json:"refusal"`
				ToolCalls json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return aicoach.Result{}, aicoach.ErrInvalidResult
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return aicoach.Result{}, ErrUnavailable
	}
	if len(envelope.Choices) != 1 {
		return aicoach.Result{}, aicoach.ErrInvalidResult
	}
	choice := envelope.Choices[0]
	switch choice.FinishReason {
	case "stop":
	case "length", "insufficient_system_resource", "aborted":
		return aicoach.Result{}, ErrIncomplete
	case "content_filter":
		return aicoach.Result{}, ErrRefused
	default:
		return aicoach.Result{}, aicoach.ErrInvalidResult
	}
	if choice.Message.Refusal != "" {
		return aicoach.Result{}, ErrRefused
	}
	if choice.Message.Role != "assistant" || choice.Message.Content == "" || len(choice.Message.Content) > 32*1024 {
		return aicoach.Result{}, aicoach.ErrInvalidResult
	}
	if calls := choice.Message.ToolCalls; len(calls) > 0 && string(calls) != "null" && string(calls) != "[]" {
		return aicoach.Result{}, aicoach.ErrInvalidResult
	}
	return aicoach.ParseResult(choice.Message.Content)
}

func transportError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnavailable
}

func token(value string, max int, model bool) bool {
	if len(value) == 0 || len(value) > max {
		return false
	}
	for _, ch := range value {
		if ch < 33 || ch > 126 {
			return false
		}
		if model && !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("_.:-", ch)) {
			return false
		}
	}
	return true
}
