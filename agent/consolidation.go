package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mosaxiv/clawlet/llm"
	"github.com/mosaxiv/clawlet/memory"
	"github.com/mosaxiv/clawlet/session"
)

type summarizeConsolidationFunc func(ctx context.Context, currentMemory, conversation string) (historyEntry, memoryUpdate string, err error)

func maybeConsolidateSession(
	ctx context.Context,
	workspace string,
	sess *session.Session,
	memoryWindow int,
	summarize summarizeConsolidationFunc,
) (bool, error) {
	if sess == nil {
		return false, nil
	}
	if summarize == nil {
		return false, nil
	}
	if memoryWindow <= 0 {
		memoryWindow = 50
	}
	oldMessages, keep, version, ok := sess.SnapshotForConsolidation(memoryWindow)
	if !ok {
		return false, nil
	}
	conversation := formatConsolidationConversation(oldMessages)
	store := memory.New(workspace)
	currentMemory := store.ReadLongTerm()

	historyEntry, memoryUpdate, err := summarize(ctx, currentMemory, conversation)
	if err != nil {
		return false, err
	}
	if !sess.ApplyConsolidation(version, keep) {
		return false, nil
	}

	if strings.TrimSpace(historyEntry) != "" {
		if err := store.AppendHistory(historyEntry); err != nil {
			return false, err
		}
	}
	memoryUpdate = strings.TrimSpace(memoryUpdate)
	if memoryUpdate != "" && memoryUpdate != strings.TrimSpace(currentMemory) {
		if err := store.WriteLongTerm(memoryUpdate + "\n"); err != nil {
			return false, err
		}
	}
	return true, nil
}

func summarizeConsolidationWithLLM(ctx context.Context, c *llm.Client, currentMemory, conversation string) (string, string, error) {
	if c == nil {
		return "", "", fmt.Errorf("llm client is nil")
	}
	prompt := buildConsolidationPrompt(currentMemory, conversation)
	res, err := c.Chat(ctx, []llm.Message{
		{Role: "system", Content: "You are a memory consolidation agent. Respond only with valid JSON."},
		{Role: "user", Content: prompt},
	}, nil)
	if err != nil {
		return "", "", err
	}

	text := strings.TrimSpace(res.Content)
	if text == "" {
		return "", "", fmt.Errorf("empty consolidation response")
	}
	if strings.HasPrefix(text, "```") {
		if i := strings.Index(text, "\n"); i >= 0 {
			text = strings.TrimSpace(text[i+1:])
		}
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}

	var parsed struct {
		HistoryEntry string `json:"history_entry"`
		MemoryUpdate string `json:"memory_update"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return "", "", fmt.Errorf("parse consolidation json: %w", err)
	}
	return strings.TrimSpace(parsed.HistoryEntry), strings.TrimSpace(parsed.MemoryUpdate), nil
}

func formatConsolidationConversation(msgs []session.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	lines := make([]string, 0, len(msgs))
	for _, m := range msgs {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		ts := strings.TrimSpace(m.Timestamp)
		if len(ts) >= 16 {
			ts = ts[:16]
		}
		role := strings.ToUpper(strings.TrimSpace(m.Role))
		if role == "" {
			role = "UNKNOWN"
		}
		toolsLabel := formatToolsLabel(m.ToolsUsed)
		if ts == "" {
			lines = append(lines, fmt.Sprintf("%s%s: %s", role, toolsLabel, content))
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s] %s%s: %s", ts, role, toolsLabel, content))
	}
	return strings.Join(lines, "\n")
}

func formatToolsLabel(names []string) string {
	if len(names) == 0 {
		return ""
	}
	tools := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		tools = append(tools, name)
	}
	if len(tools) == 0 {
		return ""
	}
	return " [tools: " + strings.Join(tools, ", ") + "]"
}

func buildConsolidationPrompt(currentMemory, conversation string) string {
	if strings.TrimSpace(currentMemory) == "" {
		currentMemory = "(empty)"
	}
	return fmt.Sprintf(`You are a memory consolidation agent. Process this conversation and return a JSON object with exactly two keys:

1. "history_entry": A paragraph (2-5 sentences) summarizing key events, decisions, and topics. Start with a timestamp like [YYYY-MM-DD HH:MM].
2. "memory_update": Updated long-term memory content. Add durable facts (preferences, profile, project context, decisions). If nothing new, return existing content unchanged.

## Current Long-term Memory
%s

## Conversation to Process
%s

Respond with ONLY valid JSON, no markdown fences.`, currentMemory, conversation)
}
