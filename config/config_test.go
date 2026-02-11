package config

import "testing"

func TestAgentDefaults_MaxTokensTemperature(t *testing.T) {
	cfg := Default()
	if cfg.Agents.Defaults.MaxTokensValue() != DefaultAgentMaxTokens {
		t.Fatalf("maxTokens=%d", cfg.Agents.Defaults.MaxTokensValue())
	}
	if cfg.Agents.Defaults.TemperatureValue() != DefaultAgentTemperature {
		t.Fatalf("temperature=%f", cfg.Agents.Defaults.TemperatureValue())
	}

	cfg.Agents.Defaults.MaxTokens = 2048
	temp := 0.0
	cfg.Agents.Defaults.Temperature = &temp
	if cfg.Agents.Defaults.MaxTokensValue() != 2048 {
		t.Fatalf("maxTokens=%d", cfg.Agents.Defaults.MaxTokensValue())
	}
	if cfg.Agents.Defaults.TemperatureValue() != 0.0 {
		t.Fatalf("temperature=%f", cfg.Agents.Defaults.TemperatureValue())
	}
}

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

func TestApplyLLMRouting_Anthropic(t *testing.T) {
	cfg := Default()
	cfg.Env["ANTHROPIC_API_KEY"] = "sk-ant-123"
	cfg.Agents.Defaults.Model = "anthropic/claude-sonnet-4-5"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, _ := cfg.ApplyLLMRouting()
	if provider != "anthropic" {
		t.Fatalf("provider=%q", provider)
	}
	if cfg.LLM.BaseURL != DefaultAnthropicBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "sk-ant-123" {
		t.Fatalf("apiKey=%q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "claude-sonnet-4-5" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}

func TestApplyLLMRouting_Gemini(t *testing.T) {
	cfg := Default()
	cfg.Env["GOOGLE_API_KEY"] = "g-123"
	cfg.Agents.Defaults.Model = "gemini/gemini-2.5-flash"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, _ := cfg.ApplyLLMRouting()
	if provider != "gemini" {
		t.Fatalf("provider=%q", provider)
	}
	if cfg.LLM.BaseURL != DefaultGeminiBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "g-123" {
		t.Fatalf("apiKey=%q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "gemini-2.5-flash" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}

func TestApplyLLMRouting_OllamaLocal(t *testing.T) {
	cfg := Default()
	cfg.Agents.Defaults.Model = "ollama/qwen2.5:14b"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, _ := cfg.ApplyLLMRouting()
	if provider != "ollama" {
		t.Fatalf("provider=%q", provider)
	}
	if cfg.LLM.BaseURL != DefaultOllamaBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "" {
		t.Fatalf("apiKey=%q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "qwen2.5:14b" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}

func TestApplyLLMRouting_LocalAlias(t *testing.T) {
	cfg := Default()
	cfg.Agents.Defaults.Model = "local/qwen2.5:14b"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, _ := cfg.ApplyLLMRouting()
	if provider != "ollama" {
		t.Fatalf("provider=%q", provider)
	}
	if cfg.LLM.BaseURL != DefaultOllamaBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.Model != "qwen2.5:14b" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}
