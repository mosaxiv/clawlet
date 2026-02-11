package llm

import "testing"

func TestAnthropicMessagesEndpoint(t *testing.T) {
	if got := anthropicMessagesEndpoint("https://api.anthropic.com"); got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("endpoint=%q", got)
	}
	if got := anthropicMessagesEndpoint("https://api.anthropic.com/v1"); got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("endpoint=%q", got)
	}
}

func TestGeminiGenerateContentEndpoint(t *testing.T) {
	if got := geminiGenerateContentEndpoint("https://generativelanguage.googleapis.com", "gemini-2.5-flash"); got != "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent" {
		t.Fatalf("endpoint=%q", got)
	}
	if got := geminiGenerateContentEndpoint("https://generativelanguage.googleapis.com/v1beta", "models/gemini-2.5-flash"); got != "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent" {
		t.Fatalf("endpoint=%q", got)
	}
}

func TestToAnthropicMessages_ToolMapping(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{
			Role:    "assistant",
			Content: "calling tool",
			ToolCalls: []ToolCallPayload{
				{
					ID:   "call_1",
					Type: "function",
					Function: ToolCallPayloadFunc{
						Name:      "read_file",
						Arguments: `{"path":"README.md"}`,
					},
				},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Content: `{"ok":true}`},
	}

	converted, system := toAnthropicMessages(msgs)
	if system != "sys" {
		t.Fatalf("system=%q", system)
	}
	if len(converted) != 3 {
		t.Fatalf("messages=%d", len(converted))
	}
	if converted[1].Role != "assistant" {
		t.Fatalf("role=%q", converted[1].Role)
	}
	if len(converted[1].Content) != 2 {
		t.Fatalf("assistant parts=%d", len(converted[1].Content))
	}
	if converted[1].Content[1].Type != "tool_use" {
		t.Fatalf("assistant part type=%q", converted[1].Content[1].Type)
	}
	if converted[2].Content[0].Type != "tool_result" {
		t.Fatalf("tool part type=%q", converted[2].Content[0].Type)
	}
}

func TestToGeminiMessages_ToolMapping(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{
			Role:    "assistant",
			Content: "calling tool",
			ToolCalls: []ToolCallPayload{
				{
					ID:   "call_1",
					Type: "function",
					Function: ToolCallPayloadFunc{
						Name:      "read_file",
						Arguments: `{"path":"README.md"}`,
					},
				},
			},
		},
		{Role: "tool", Name: "read_file", ToolCallID: "call_1", Content: `{"ok":true}`},
	}

	converted, system := toGeminiMessages(msgs)
	if system != "sys" {
		t.Fatalf("system=%q", system)
	}
	if len(converted) != 3 {
		t.Fatalf("messages=%d", len(converted))
	}
	if converted[1].Role != "model" {
		t.Fatalf("role=%q", converted[1].Role)
	}
	if len(converted[1].Parts) != 2 {
		t.Fatalf("model parts=%d", len(converted[1].Parts))
	}
	if converted[1].Parts[1].FunctionCall == nil {
		t.Fatalf("functionCall=nil")
	}
	if converted[2].Parts[0].FunctionResponse == nil {
		t.Fatalf("functionResponse=nil")
	}
}
