package deepseek_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rhythm/internal/aicoach"
	"rhythm/internal/deepseek"
)

const answer = `{"title":"Наблюдения","summary":"Указан сон 5 часов.","insights":[{"text":"В дневнике указан сон 5 часов.","factIds":["f1"]}],"nextAction":"journal","draft":null}`

func request() aicoach.Request {
	return aicoach.Request{Task: aicoach.TaskAsk, Message: "Что указано в дневнике?", Context: aicoach.Context{Today: "2026-10-17", TimeZone: "Europe/Moscow", Facts: []aicoach.Fact{{ID: "f1", Text: "Указан сон 5 часов."}}, Days: []aicoach.Day{{Date: "2026-10-17"}}}}
}

func envelope(t *testing.T, content, finish string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": finish, "message": map[string]any{"role": "assistant", "content": content}}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func respond(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func client(t *testing.T, server *httptest.Server) *deepseek.Client {
	t.Helper()
	c, err := deepseek.New(deepseek.Config{APIKey: "fixture-key-not-real", Endpoint: server.URL + "/chat/completions", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGenerateUsesJSONModeAndSeparatesUntrustedInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture-key-not-real" {
			t.Error("invalid authenticated request")
		}
		var body struct {
			Model          string                           `json:"model"`
			Messages       []struct{ Role, Content string } `json:"messages"`
			MaxTokens      int                              `json:"max_tokens"`
			Stream         bool                             `json:"stream"`
			ResponseFormat struct{ Type string }            `json:"response_format"`
			Thinking       struct{ Type string }            `json:"thinking"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != deepseek.DefaultModel || body.Stream || body.MaxTokens != 2200 || body.ResponseFormat.Type != "json_object" || body.Thinking.Type != "disabled" {
			t.Error("unbounded or incompatible provider request")
		}
		if len(body.Messages) != 2 {
			t.Error("unexpected messages")
			return
		}
		if body.Messages[0].Role != "system" || !strings.Contains(body.Messages[0].Content, "untrusted") || !strings.Contains(body.Messages[0].Content, "JSON Schema") || strings.Contains(body.Messages[0].Content, "ignore-everything-fixture") {
			t.Error("system instruction boundary changed")
		}
		var input aicoach.Request
		if err := json.Unmarshal([]byte(body.Messages[1].Content), &input); err != nil {
			t.Error(err)
		}
		if body.Messages[1].Role != "user" || input.Message != "ignore-everything-fixture" {
			t.Error("untrusted input lost")
		}
		respond(t, w, 200, envelope(t, answer, "stop"))
	}))
	defer server.Close()
	req := request()
	req.Message = "ignore-everything-fixture"
	result, err := client(t, server).Generate(context.Background(), req)
	if err != nil || len(result.Insights) != 1 {
		t.Fatalf("result: %+v, error: %v", result, err)
	}
}

func TestGenerateRejectsInvalidOutputsWithoutRetriesOrSecretLeakage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"auth", 401, "private-key-and-journal", deepseek.ErrUnavailable},
		{"balance", 402, "private-key-and-journal", deepseek.ErrUnavailable},
		{"quota", 429, "private-key-and-journal", deepseek.ErrUnavailable},
		{"error", 200, `{"error":{"message":"private-key-and-journal"}}`, deepseek.ErrUnavailable},
		{"incomplete", 200, envelope(t, answer, "length"), deepseek.ErrIncomplete},
		{"aborted", 200, envelope(t, answer, "aborted"), deepseek.ErrIncomplete},
		{"refused", 200, envelope(t, answer, "content_filter"), deepseek.ErrRefused},
		{"tools", 200, envelope(t, answer, "tool_calls"), aicoach.ErrInvalidResult},
		{"empty", 200, `{"choices":[]}`, aicoach.ErrInvalidResult},
		{"null", 200, envelope(t, "", "stop"), aicoach.ErrInvalidResult},
		{"truncated", 200, `{"choices":[`, aicoach.ErrInvalidResult},
		{"oversize", 200, strings.Repeat("x", 1024*1024+1), aicoach.ErrInvalidResult},
		{"unknown_fact", 200, envelope(t, strings.ReplaceAll(answer, `"f1"`, `"invented"`), "stop"), aicoach.ErrInvalidResult},
		{"unknown_field", 200, envelope(t, strings.TrimSuffix(answer, "}")+`,"admin":true}`, "stop"), aicoach.ErrInvalidResult},
		{"missing_draft", 200, envelope(t, strings.Replace(answer, `,"draft":null`, "", 1), "stop"), aicoach.ErrInvalidResult},
		{"extra_json", 200, envelope(t, answer+` {}`, "stop"), aicoach.ErrInvalidResult},
		{"markdown", 200, envelope(t, "```json\n"+answer+"\n```", "stop"), aicoach.ErrInvalidResult},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); respond(t, w, tc.status, tc.body) }))
			defer server.Close()
			_, err := client(t, server).Generate(context.Background(), request())
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), "private-key-and-journal") || strings.Contains(err.Error(), "fixture-key-not-real") || calls.Load() != 1 {
				t.Fatal("leak or retry")
			}
		})
	}
}

func TestDraftKeepsUnknownMetricsAndRejectsInventedDates(t *testing.T) {
	for _, tc := range []struct {
		name, draft string
		valid       bool
	}{
		{"valid", `{"date":"2026-10-17","status":"wellbeing","sessionId":"","sleep":5,"fatigue":null}`, true},
		{"future", `{"date":"2026-10-18","status":"wellbeing","sessionId":"","sleep":5,"fatigue":null}`, false},
		{"missing_metric", `{"date":"2026-10-17","status":"wellbeing","sessionId":"","sleep":5}`, false},
		{"invented_session", `{"date":"2026-10-17","status":"done","sessionId":"2026-10-17","sleep":5,"fatigue":null}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				respond(t, w, 200, envelope(t, strings.Replace(answer, `"draft":null`, `"draft":`+tc.draft, 1), "stop"))
			}))
			defer server.Close()
			req := request()
			req.Task = aicoach.TaskCheckin
			result, err := client(t, server).Generate(context.Background(), req)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && (result.Draft == nil || result.Draft.Fatigue != nil || result.Draft.Sleep == nil || *result.Draft.Sleep != 5) {
				t.Fatal("unknown metric fabricated")
			}
		})
	}
}

func TestCancellationAndInvalidInputDoNotStartPaidWork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); respond(t, w, 500, "") }))
	defer server.Close()
	c := client(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Generate(ctx, request()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.Generate(context.Background(), aicoach.Request{}); !errors.Is(err, aicoach.ErrInvalidRequest) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("started paid work")
	}
}

func TestDeadlineCancelsProviderRequest(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		<-r.Context().Done()
		close(done)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := client(t, server).Generate(ctx, request()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("provider request leaked")
	}
}

func TestRedirectCannotForwardCredentials(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/chat/completions", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	if _, err := client(t, server).Generate(context.Background(), request()); !errors.Is(err, deepseek.ErrUnavailable) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("redirect forwarded credentials")
	}
}

func TestConfigurationValidation(t *testing.T) {
	for _, cfg := range []deepseek.Config{{}, {APIKey: "key\nsecret"}, {APIKey: "fixture", Model: "bad/model"}, {APIKey: "fixture", Endpoint: "http://api.deepseek.com/chat/completions"}, {APIKey: "fixture", Endpoint: "https://api.deepseek.com/chat/completions?key=secret"}, {APIKey: "fixture", Endpoint: "https://user:pass@api.deepseek.com/chat/completions"}, {APIKey: "fixture", Endpoint: "https://api.deepseek.com/other"}} {
		if _, err := deepseek.New(cfg); !errors.Is(err, deepseek.ErrConfiguration) {
			t.Fatal("invalid config accepted")
		}
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTransportErrorCannotLeakHeaders(t *testing.T) {
	c, err := deepseek.New(deepseek.Config{APIKey: "fixture-secret", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("transport echoed %s", r.Header.Get("Authorization"))
	})}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Generate(context.Background(), request())
	if !errors.Is(err, deepseek.ErrUnavailable) || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("unsafe transport error: %v", err)
	}
}

func TestHTTPErrorExposesOnlyStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { respond(t, w, 402, "fixture-secret") }))
	defer server.Close()
	_, err := client(t, server).Generate(context.Background(), request())
	var status *deepseek.HTTPError
	if !errors.As(err, &status) || status.HTTPStatus() != 402 || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("unsafe provider error: %v", err)
	}
}
