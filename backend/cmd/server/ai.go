package main

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"rhythm/internal/deepseek"
	"rhythm/internal/httpapi"
	"rhythm/internal/openai"
)

type aiConfig struct {
	client   httpapi.AIGenerator
	provider string
	model    string
	timeout  time.Duration
}

// Selection is explicit when both credentials exist. A key never reaches a
// different provider as a fallback; missing credentials leave the coach off.
func configureAI(getenv func(string) string) (aiConfig, error) {
	provider := getenv("AI_PROVIDER")
	if provider == "" {
		if getenv("DEEPSEEK_API_KEY") != "" && getenv("OPENAI_API_KEY") != "" {
			return aiConfig{}, errors.New("set AI_PROVIDER when both AI credentials are configured")
		}
		provider = "openai"
		if getenv("DEEPSEEK_API_KEY") != "" {
			provider = "deepseek"
		}
	}
	var c aiConfig
	var err error
	switch provider {
	case "deepseek":
		c.provider, c.model, c.timeout = "DeepSeek", getenv("DEEPSEEK_MODEL"), 20*time.Second
		if c.model == "" {
			c.model = deepseek.DefaultModel
		}
		if key := getenv("DEEPSEEK_API_KEY"); key != "" {
			c.client, err = deepseek.New(deepseek.Config{APIKey: key, Model: c.model})
		}
	case "openai":
		c.provider, c.model, c.timeout = "OpenAI", getenv("OPENAI_MODEL"), 3*time.Second
		if c.model == "" {
			c.model = openai.DefaultModel
		}
		if key := getenv("OPENAI_API_KEY"); key != "" {
			c.client, err = openai.New(openai.Config{APIKey: key, Model: c.model})
		}
	default:
		return aiConfig{}, errors.New("AI_PROVIDER must be openai or deepseek")
	}
	if err != nil {
		return aiConfig{}, fmt.Errorf("configure AI: %w", err)
	}
	if raw := getenv("AI_TIMEOUT_SECONDS"); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds < 1 || seconds > 30 {
			return aiConfig{}, errors.New("AI_TIMEOUT_SECONDS must be an integer between 1 and 30")
		}
		c.timeout = time.Duration(seconds) * time.Second
	}
	return c, nil
}
