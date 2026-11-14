// Package openai is the HTTP adapter for the optional AI coach. It sends no
// credentials or provider response bodies to the domain or application logs.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"rhythm/internal/aicoach"
)

const (
	DefaultModel     = "gpt-4.1-mini"
	defaultEndpoint  = "https://api.openai.com/v1/responses"
	maxResponseBytes = 1024 * 1024
)

var (
	ErrConfiguration = errors.New("неверная конфигурация AI-провайдера")
	ErrUnavailable   = errors.New("AI-провайдер сейчас недоступен")
	ErrRefused       = errors.New("AI не смог выполнить этот запрос")
	ErrIncomplete    = errors.New("AI не завершил ответ; данные не изменены")
)

type Config struct {
	APIKey string
	Model  string
	// HTTPClient and Endpoint are injection points for controlled tests. The
	// application composition root uses only APIKey and Model in production.
	HTTPClient *http.Client
	Endpoint   string
}

// Client is safe for concurrent use. It owns an immutable HTTP client copy;
// callers own request values and must not modify their slices during Generate.
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
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/v1/responses" {
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
	// Even injected clients cannot forward the key through a provider redirect.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{apiKey: cfg.APIKey, model: cfg.Model, endpoint: endpoint, httpClient: &client}, nil
}

// Generate performs one provider request, without retries or persistence. The
// caller controls deadline, consent, rate limits and confirmation of a draft.
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
	wire := responseRequest{Model: c.model, Store: false, Instructions: aicoach.Instructions, Input: string(data), MaxOutputTokens: 2200}
	wire.Text.Format = responseFormat{Type: "json_schema", Name: "rhythm_coach", Strict: true, Schema: json.RawMessage(aicoach.ResultSchema)}
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
		// Error bodies can echo a prompt or credentials. Do not read, return or
		// log them. Closing prevents an unbounded provider response being read.
		if err := resp.Body.Close(); err != nil {
			return aicoach.Result{}, transportError(ctx)
		}
		return aicoach.Result{}, transportError(ctx)
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

type responseRequest struct {
	Model           string `json:"model"`
	Store           bool   `json:"store"`
	Instructions    string `json:"instructions"`
	Input           string `json:"input"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Text            struct {
		Format responseFormat `json:"format"`
	} `json:"text"`
}

type responseFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type responseEnvelope struct {
	Status            string          `json:"status"`
	Error             json.RawMessage `json:"error"`
	IncompleteDetails json.RawMessage `json:"incomplete_details"`
	Output            []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Status  string `json:"status"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func parseResponse(data []byte) (aicoach.Result, error) {
	var envelope responseEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return aicoach.Result{}, aicoach.ErrInvalidResult
	}
	if envelope.Status == "incomplete" || nonNull(envelope.IncompleteDetails) {
		return aicoach.Result{}, ErrIncomplete
	}
	if nonNull(envelope.Error) || envelope.Status == "failed" || envelope.Status == "cancelled" || envelope.Status == "queued" || envelope.Status == "in_progress" {
		return aicoach.Result{}, ErrUnavailable
	}
	if envelope.Status != "completed" {
		return aicoach.Result{}, aicoach.ErrInvalidResult
	}
	text := ""
	for _, output := range envelope.Output {
		if output.Type == "reasoning" {
			continue // Some configured models expose reasoning before the message.
		}
		if output.Type != "message" || output.Role != "assistant" || output.Status != "completed" {
			return aicoach.Result{}, aicoach.ErrInvalidResult
		}
		for _, content := range output.Content {
			if content.Type == "refusal" {
				return aicoach.Result{}, ErrRefused
			}
			if content.Type != "output_text" || text != "" || content.Text == "" {
				return aicoach.Result{}, aicoach.ErrInvalidResult
			}
			text = content.Text
		}
	}
	if text == "" || len(text) > 32*1024 {
		return aicoach.Result{}, aicoach.ErrInvalidResult
	}
	return aicoach.ParseResult(text)
}

func nonNull(value json.RawMessage) bool { return len(value) > 0 && string(value) != "null" }
