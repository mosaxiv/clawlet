package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mosaxiv/picoclaw/config"
	"github.com/mosaxiv/picoclaw/paths"
)

func loadConfig() (*config.Config, string, error) {
	cfgPath, err := paths.ConfigPath()
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, cfgPath, fmt.Errorf("failed to load config: %s\nhint: run `picoclaw onboard`\n%w", cfgPath, err)
	}

	applyEnvOverrides(cfg)
	cfg.ApplyLLMRouting()

	if strings.TrimSpace(cfg.LLM.APIKey) == "" {
		fmt.Fprintln(os.Stderr, "warning: llm.apiKey is empty (set in config.env or env vars)")
	}

	return cfg, cfgPath, nil
}

func applyEnvOverrides(cfg *config.Config) {
	if v := os.Getenv("PICOCLAW_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("PICOCLAW_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("PICOCLAW_MODEL"); v != "" {
		cfg.Agents.Defaults.Model = v
	}
	if v := os.Getenv("PICOCLAW_OPENAI_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENAI_API_KEY"] = v
	}
	if v := os.Getenv("PICOCLAW_OPENROUTER_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENROUTER_API_KEY"] = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENAI_API_KEY"] = v
	}
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENROUTER_API_KEY"] = v
	}

	if cfg.LLM.Headers == nil {
		cfg.LLM.Headers = map[string]string{}
	}
}

func resolveWorkspace(flagValue string) (string, error) {
	ws := strings.TrimSpace(flagValue)
	if ws == "" {
		if v := strings.TrimSpace(os.Getenv("PICOCLAW_WORKSPACE")); v != "" {
			ws = v
		} else {
			ws = paths.WorkspaceDir()
		}
	}
	return filepath.Abs(ws)
}
