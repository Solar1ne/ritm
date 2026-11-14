package main

import (
	"testing"
	"time"
)

func TestAIProviderSelection(t *testing.T) {
	for _, tc := range []struct {
		name            string
		env             map[string]string
		provider, model string
		timeout         time.Duration
		enabled, fail   bool
	}{
		{name: "disabled", provider: "OpenAI", model: "gpt-4.1-mini", timeout: 3 * time.Second},
		{name: "openai", env: map[string]string{"OPENAI_API_KEY": "fixture"}, provider: "OpenAI", model: "gpt-4.1-mini", timeout: 3 * time.Second, enabled: true},
		{name: "deepseek", env: map[string]string{"DEEPSEEK_API_KEY": "fixture"}, provider: "DeepSeek", model: "deepseek-flash", timeout: 20 * time.Second, enabled: true},
		{name: "explicit", env: map[string]string{"AI_PROVIDER": "deepseek", "DEEPSEEK_API_KEY": "fixture", "OPENAI_API_KEY": "other", "DEEPSEEK_MODEL": "deepseek-v4-pro", "AI_TIMEOUT_SECONDS": "25"}, provider: "DeepSeek", model: "deepseek-v4-pro", timeout: 25 * time.Second, enabled: true},
		{name: "no_key_fallback", env: map[string]string{"AI_PROVIDER": "deepseek", "OPENAI_API_KEY": "fixture"}, provider: "DeepSeek", model: "deepseek-flash", timeout: 20 * time.Second},
		{name: "ambiguous", env: map[string]string{"DEEPSEEK_API_KEY": "fixture", "OPENAI_API_KEY": "fixture"}, fail: true},
		{name: "unknown", env: map[string]string{"AI_PROVIDER": "other"}, fail: true},
		{name: "invalid_key", env: map[string]string{"DEEPSEEK_API_KEY": "bad\nkey"}, fail: true},
		{name: "timeout_low", env: map[string]string{"AI_TIMEOUT_SECONDS": "0"}, fail: true},
		{name: "timeout_high", env: map[string]string{"AI_TIMEOUT_SECONDS": "31"}, fail: true},
		{name: "timeout_nan", env: map[string]string{"AI_TIMEOUT_SECONDS": "forever"}, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := configureAI(func(key string) string { return tc.env[key] })
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v", err)
			}
			if tc.fail {
				return
			}
			if got.provider != tc.provider || got.model != tc.model || got.timeout != tc.timeout || (got.client != nil) != tc.enabled {
				t.Fatalf("unexpected configuration: provider=%s model=%s timeout=%s enabled=%v", got.provider, got.model, got.timeout, got.client != nil)
			}
		})
	}
}
