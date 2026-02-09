package config

import "testing"

func TestApplyLLMRouting_OpenRouter(t *testing.T) {
	cfg := Default()
	cfg.Env["OPENROUTER_API_KEY"] = "sk-or-123"
	cfg.Agents.Defaults.Model = "openrouter/anthropic/claude-sonnet-4-5"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, configured := cfg.ApplyLLMRouting()
	if provider != "openrouter" {
		t.Fatalf("provider=%q", provider)
	}
	if configured != "openrouter/anthropic/claude-sonnet-4-5" {
		t.Fatalf("configured=%q", configured)
	}
	if cfg.LLM.BaseURL != DefaultOpenRouterBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "sk-or-123" {
		t.Fatalf("apiKey=%q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}

func TestApplyLLMRouting_OpenAI(t *testing.T) {
	cfg := Default()
	cfg.Env["OPENAI_API_KEY"] = "sk-123"
	cfg.Agents.Defaults.Model = "openai/gpt-4o-mini"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, _ := cfg.ApplyLLMRouting()
	if provider != "openai" {
		t.Fatalf("provider=%q", provider)
	}
	if cfg.LLM.BaseURL != DefaultOpenAIBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "sk-123" {
		t.Fatalf("apiKey=%q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "gpt-4o-mini" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}
