package telegrambot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCommandFiltering(t *testing.T) {
	for _, tt := range []struct {
		text, chat string
		bot, want  bool
	}{
		{"/start", "private", false, true}, {"/help@Rhythm_bot", "private", false, true}, {"/privacy", "private", false, true},
		{"/start@other", "private", false, false}, {"hello", "private", false, false}, {"/start", "group", false, false}, {"/start", "private", true, false},
	} {
		t.Run(tt.text+tt.chat, func(t *testing.T) {
			m := &message{Text: tt.text}
			m.Chat.ID = 1
			m.Chat.Type = tt.chat
			m.From.Bot = tt.bot
			if (reply(m, "rhythm_bot") != "") != tt.want {
				t.Fatal("incorrect response filter")
			}
		})
	}
}

func TestPollingOffsetAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls, sends := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			fmt.Fprint(w, `{"ok":true,"result":{"username":"rhythm_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			fmt.Fprint(w, `{"ok":true,"result":{"url":""}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			calls++
			var payload struct {
				Offset int64 `json:"offset"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if calls == 1 {
				fmt.Fprint(w, `{"ok":true,"result":[{"update_id":5,"message":{"text":"/start","chat":{"id":7,"type":"private"}}},{"update_id":5,"message":{"text":"/start","chat":{"id":7,"type":"private"}}},{"update_id":6,"message":{"text":"random","chat":{"id":7,"type":"private"}}}]}`)
			} else {
				if payload.Offset != 7 {
					t.Errorf("offset %d", payload.Offset)
				}
				cancel()
				fmt.Fprint(w, `{"ok":true,"result":[]}`)
			}
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			sends++
			fmt.Fprint(w, `{"ok":true,"result":{}}`)
		default:
			t.Error("unexpected method")
		}
	}))
	defer server.Close()
	client, err := NewClient("123:fixture", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.base = server.URL
	if err = Run(ctx, client, "https://rhythm.example"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatalf("sent %d replies", sends)
	}
}

func TestErrorRedactionAndWebhookRefusal(t *testing.T) {
	for _, webhook := range []bool{false, true} {
		t.Run(fmt.Sprint(webhook), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !webhook {
					w.WriteHeader(401)
					fmt.Fprint(w, "SECRET")
					return
				}
				if strings.HasSuffix(r.URL.Path, "getMe") {
					fmt.Fprint(w, `{"ok":true,"result":{"username":"bot"}}`)
				} else {
					fmt.Fprint(w, `{"ok":true,"result":{"url":"https://existing.example"}}`)
				}
			}))
			defer server.Close()
			client, _ := NewClient("123:SECRET", server.Client())
			client.base = server.URL
			err := Run(context.Background(), client, "https://rhythm.example")
			if err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("unsafe error: %v", err)
			}
		})
	}
}

func TestCancellationDuringLongPoll(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"username":"bot"}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "getWebhookInfo") {
			fmt.Fprint(w, `{"ok":true,"result":{"url":""}}`)
			return
		}
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	client, _ := NewClient("123:fixture", server.Client())
	client.base = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, client, "https://rhythm.example") }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("poll did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("poll did not stop")
	}
}

func TestMiniURL(t *testing.T) {
	for _, origin := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/path", "https://example.com?x=1"} {
		if _, err := MiniURL(origin); err == nil {
			t.Fatal(origin)
		}
	}
}

func TestResponseLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte(strings.Repeat("x", (1<<20)+1)))
		if err != nil {
			return
		}
	}))
	defer server.Close()
	client, err := NewClient("123:fixture", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.base = server.URL
	if err = client.call(context.Background(), "getMe", struct{}{}, nil); err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("limit error: %v", err)
	}
}

func TestBackoffIsCancelable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if err := wait(ctx, 30*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancellation waited for backoff")
	}
}
