package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	Provider    string
	BaseURL     string
	APIKey      string
	Model       string
	MaxTokens   int
	Temperature *float64
	Headers     map[string]string
	HTTP        HTTPDoer
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ChatResult struct {
	Content   string
	ToolCalls []ToolCall
}

func (r ChatResult) HasToolCalls() bool { return len(r.ToolCalls) > 0 }

func (c *Client) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResult, error) {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 120 * time.Second}
	}
	switch normalizeProvider(c.Provider) {
	case "", "openai", "openrouter", "ollama","shengsuanyun":
		return c.chatOpenAICompatible(ctx, messages, tools)
	case "anthropic":
		return c.chatAnthropic(ctx, messages, tools)
	case "gemini":
		return c.chatGemini(ctx, messages, tools)
	case "openai-codex":
		return c.chatOpenAICodex(ctx, messages, tools)
	default:
		return nil, fmt.Errorf("unsupported llm provider: %s", strings.TrimSpace(c.Provider))
	}
}

func normalizeProvider(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "local":
		return "ollama"
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

func (c *Client) maxTokensValue() int {
	if c.MaxTokens <= 0 {
		return 8192
	}
	return c.MaxTokens
}

func (c *Client) temperatureValue() *float64 {
	if c.Temperature != nil {
		v := *c.Temperature
		return &v
	}
	v := 0.7
	return &v
}
