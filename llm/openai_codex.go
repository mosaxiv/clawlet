package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultCodexBaseURL      = "https://chatgpt.com/backend-api"
	defaultCodexModel        = "gpt-5.2"
	defaultCodexInstructions = "You are Codex, a coding assistant."
)

type codexRequest struct {
	Model             string           `json:"model"`
	Store             bool             `json:"store"`
	Stream            bool             `json:"stream"`
	Instructions      string           `json:"instructions"`
	Input             []codexInputItem `json:"input"`
	Text              codexTextConfig  `json:"text"`
	Include           []string         `json:"include,omitempty"`
	PromptCacheKey    string           `json:"prompt_cache_key,omitempty"`
	ToolChoice        string           `json:"tool_choice,omitempty"`
	ParallelToolCalls bool             `json:"parallel_tool_calls,omitempty"`
	Tools             []codexTool      `json:"tools,omitempty"`
}

type codexTextConfig struct {
	Verbosity string `json:"verbosity,omitempty"`
}

type codexTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type codexInputItem struct {
	Type      string              `json:"type,omitempty"`
	Role      string              `json:"role,omitempty"`
	Status    string              `json:"status,omitempty"`
	ID        string              `json:"id,omitempty"`
	Content   []codexInputContent `json:"content,omitempty"`
	CallID    string              `json:"call_id,omitempty"`
	Name      string              `json:"name,omitempty"`
	Arguments string              `json:"arguments,omitempty"`
	Output    string              `json:"output,omitempty"`
}

type codexInputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func (c *Client) chatOpenAICodex(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResult, error) {
	tok, err := LoadCodexOAuthToken()
	if err != nil {
		return nil, err
	}

	systemPrompt, inputItems := toCodexInput(messages)
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = defaultCodexInstructions
	}
	endpoint := codexResponsesEndpoint(c.BaseURL)
	model := resolveCodexModel(c.Model)
	reqBody := codexRequest{
		Model:        model,
		Store:        false,
		Stream:       true,
		Instructions: systemPrompt,
		Input:        inputItems,
		Text: codexTextConfig{
			Verbosity: "medium",
		},
		Include:           []string{"reasoning.encrypted_content"},
		PromptCacheKey:    codexPromptCacheKey(messages),
		ToolChoice:        "auto",
		ParallelToolCalls: true,
	}

	if len(tools) > 0 {
		convertedTools, err := toCodexTools(tools)
		if err != nil {
			return nil, err
		}
		reqBody.Tools = convertedTools
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("chatgpt-account-id", tok.AccountID)
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", codexOAuthOriginator)
	req.Header.Set("User-Agent", "clawlet (go)")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		return nil, fmt.Errorf("codex http %d: %s", resp.StatusCode, codexFriendlyError(resp.StatusCode, strings.TrimSpace(string(raw))))
	}

	return consumeCodexSSE(resp.Body)
}

type codexSSEEvent struct {
	Type      string          `json:"type"`
	Delta     string          `json:"delta"`
	CallID    string          `json:"call_id"`
	Arguments json.RawMessage `json:"arguments"`
	Item      struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"item"`
}

type codexToolCallBuffer struct {
	ItemID    string
	Name      string
	Arguments string
}

func consumeCodexSSE(r io.Reader) (*ChatResult, error) {
	out := &ChatResult{}
	buffers := map[string]*codexToolCallBuffer{}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 2<<20)
	dataLines := make([]string, 0, 2)

	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if data == "" || data == "[DONE]" {
			return nil
		}
		return handleCodexSSEData(data, out, buffers)
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			dataLines = append(dataLines, strings.TrimSpace(after))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}

	return out, nil
}

func handleCodexSSEData(data string, out *ChatResult, buffers map[string]*codexToolCallBuffer) error {
	var evt codexSSEEvent
	if err := json.Unmarshal([]byte(data), &evt); err != nil {
		// Ignore non-JSON chunks.
		return nil
	}
	switch evt.Type {
	case "response.output_text.delta":
		out.Content += evt.Delta
	case "response.output_item.added":
		if evt.Item.Type != "function_call" {
			return nil
		}
		callID := strings.TrimSpace(evt.Item.CallID)
		if callID == "" {
			return nil
		}
		buffers[callID] = &codexToolCallBuffer{
			ItemID:    strings.TrimSpace(evt.Item.ID),
			Name:      strings.TrimSpace(evt.Item.Name),
			Arguments: rawToCodexArgString(evt.Item.Arguments),
		}
	case "response.function_call_arguments.delta":
		callID := strings.TrimSpace(evt.CallID)
		if callID == "" {
			return nil
		}
		buf := buffers[callID]
		if buf == nil {
			buf = &codexToolCallBuffer{}
			buffers[callID] = buf
		}
		buf.Arguments += evt.Delta
	case "response.function_call_arguments.done":
		callID := strings.TrimSpace(evt.CallID)
		if callID == "" {
			return nil
		}
		buf := buffers[callID]
		if buf == nil {
			buf = &codexToolCallBuffer{}
			buffers[callID] = buf
		}
		if args := rawToCodexArgString(evt.Arguments); args != "" {
			buf.Arguments = args
		}
	case "response.output_item.done":
		if evt.Item.Type != "function_call" {
			return nil
		}
		callID := strings.TrimSpace(evt.Item.CallID)
		if callID == "" {
			return nil
		}
		buf := buffers[callID]
		if buf == nil {
			buf = &codexToolCallBuffer{}
		}
		if name := strings.TrimSpace(evt.Item.Name); name != "" {
			buf.Name = name
		}
		if itemID := strings.TrimSpace(evt.Item.ID); itemID != "" {
			buf.ItemID = itemID
		}
		if args := rawToCodexArgString(evt.Item.Arguments); args != "" {
			buf.Arguments = args
		}

		itemID := strings.TrimSpace(buf.ItemID)
		if itemID == "" {
			itemID = "fc_0"
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        callID + "|" + itemID,
			Name:      strings.TrimSpace(buf.Name),
			Arguments: codexArgumentsToJSON(buf.Arguments),
		})
		delete(buffers, callID)
	case "error", "response.failed":
		return fmt.Errorf("codex response failed")
	}
	return nil
}

func toCodexTools(tools []ToolDefinition) ([]codexTool, error) {
	out := make([]codexTool, 0, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(t.Function.Name)
		if name == "" {
			continue
		}
		params, err := schemaToRawJSON(t.Function.Parameters)
		if err != nil {
			return nil, fmt.Errorf("codex tool schema %s: %w", name, err)
		}
		out = append(out, codexTool{
			Type:        "function",
			Name:        name,
			Description: t.Function.Description,
			Parameters:  params,
		})
	}
	return out, nil
}

func toCodexInput(messages []Message) (string, []codexInputItem) {
	systemPrompt := ""
	input := make([]codexInputItem, 0, len(messages))

	for i, m := range messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		switch role {
		case "system":
			systemPrompt = m.Content
		case "user":
			userText := codexMessageText(m)
			if strings.TrimSpace(userText) == "" {
				continue
			}
			input = append(input, codexInputItem{
				Role: "user",
				Content: []codexInputContent{
					{Type: "input_text", Text: userText},
				},
			})
		case "assistant":
			assistantText := codexMessageText(m)
			if strings.TrimSpace(assistantText) != "" {
				input = append(input, codexInputItem{
					Type:   "message",
					Role:   "assistant",
					Status: "completed",
					ID:     fmt.Sprintf("msg_%d", i),
					Content: []codexInputContent{
						{Type: "output_text", Text: assistantText},
					},
				})
			}
			for _, tc := range m.ToolCalls {
				callID, itemID := splitCodexToolCallID(tc.ID)
				if strings.TrimSpace(callID) == "" {
					callID = fmt.Sprintf("call_%d", i)
				}
				if strings.TrimSpace(itemID) == "" {
					itemID = fmt.Sprintf("fc_%d", i)
				}
				args := strings.TrimSpace(tc.Function.Arguments)
				if args == "" {
					args = "{}"
				}
				input = append(input, codexInputItem{
					Type:      "function_call",
					ID:        itemID,
					CallID:    callID,
					Name:      tc.Function.Name,
					Arguments: args,
				})
			}
		case "tool":
			callID, _ := splitCodexToolCallID(m.ToolCallID)
			input = append(input, codexInputItem{
				Type:   "function_call_output",
				CallID: callID,
				Output: m.Content,
			})
		}
	}

	return systemPrompt, input
}

func codexMessageText(m Message) string {
	if strings.TrimSpace(m.Content) != "" {
		return m.Content
	}
	if len(m.Parts) == 0 {
		return ""
	}
	chunks := make([]string, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Type {
		case ContentPartTypeText:
			if strings.TrimSpace(p.Text) != "" {
				chunks = append(chunks, p.Text)
			}
		case ContentPartTypeImage:
			label := strings.TrimSpace(p.Name)
			if label == "" {
				label = "attached image"
			}
			chunks = append(chunks, "["+label+"]")
		}
	}
	return strings.TrimSpace(strings.Join(chunks, "\n"))
}

func splitCodexToolCallID(id string) (callID string, itemID string) {
	v := strings.TrimSpace(id)
	if v == "" {
		return "call_0", ""
	}
	if strings.Contains(v, "|") {
		parts := strings.SplitN(v, "|", 2)
		return parts[0], parts[1]
	}
	return v, ""
}

func stripCodexModelPrefix(model string) string {
	m := strings.TrimSpace(model)
	if after, ok := strings.CutPrefix(m, "openai-codex/"); ok {
		return after
	}
	return m
}

func resolveCodexModel(model string) string {
	m := strings.ToLower(stripCodexModelPrefix(model))
	if m == "" {
		return defaultCodexModel
	}
	if strings.Contains(m, "/") {
		return defaultCodexModel
	}
	if strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") {
		return m
	}
	return defaultCodexModel
}

func codexResponsesEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = defaultCodexBaseURL
	}
	if strings.HasSuffix(base, "/codex") {
		return base + "/responses"
	}
	if strings.HasSuffix(base, "/codex/responses") {
		return base
	}
	return base + "/codex/responses"
}

func codexPromptCacheKey(messages []Message) string {
	b, err := json.Marshal(messages)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func rawToCodexArgString(v json.RawMessage) string {
	if len(v) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s
	}
	return string(v)
}

func codexArgumentsToJSON(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	b, _ := json.Marshal(map[string]string{"raw": trimmed})
	return b
}

func codexFriendlyError(statusCode int, raw string) string {
	if statusCode == http.StatusTooManyRequests {
		return "usage quota exceeded or rate limited; try again later"
	}
	if raw == "" {
		return http.StatusText(statusCode)
	}
	return raw
}

func schemaToRawJSON(s JSONSchema) (json.RawMessage, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage(`{"type":"object"}`), nil
	}
	return json.RawMessage(trimmed), nil
}
